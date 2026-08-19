"""把 warp 工具调用映射到 Harbor BaseEnvironment 的执行器。

设计要点:
- 容器里不假设有 rg/python3:搜索一律用 grep/find(POSIX),文件写回用
  base64(首选)或 python3(兜底),两者都没有则按文件报错。
- run_shell_command 统一包成 nohup 后台进程 + 日志文件 + exit 标记文件,
  同步等待 sync_wait 秒;超时未完成就回 LongRunningShellCommandSnapshot
  (带上我们签发的 command_id),之后 read_shell_command_output 轮询同一
  日志/标记文件。这样长短命令语义统一,也不依赖 environment.exec 的超时行为。
- apply_file_diffs 的 search/replace 在本客户端侧对字节串完成,容器只负责
  读(base64 出)写(base64 回),避免在容器内拼 sed/补丁解释器。
- 所有输出经过截断(output_cap,默认 60KB):评测配置里 memory.enabled=false,
  服务端不做上下文压缩,必须防止单次结果撑爆模型上下文窗口。
"""

from __future__ import annotations

import asyncio
import base64
import logging
import posixpath
import re
import shlex
import uuid
from typing import Optional

from google.protobuf import empty_pb2

from tb_client.pb import file_content_pb2, request_pb2

log = logging.getLogger(__name__)

# 无用户终端的固定提示(transfer_shell_command_control_to_user 用)
NO_USER_TERMINAL_NOTE = (
    "[warp-local-eval] No interactive user terminal in this eval environment; "
    "the command keeps running in the background. "
    "Use read_shell_command_output to poll its progress."
)

_LINE_RE = re.compile(r"^(.*?):([0-9]+):(.*)$")


class ContainerExecutor:
    """在一个 Harbor 环境(容器)里执行 warp 的 9 种客户端工具。"""

    def __init__(
        self,
        environment,
        workdir: str,
        *,
        sync_wait: int = 120,
        poll_cap: int = 180,
        output_cap: int = 60_000,
        grep_line_cap: int = 300,
        glob_cap: int = 500,
        state_dir: str = "",
    ):
        self._env = environment
        self.workdir = workdir or "/"
        self.sync_wait = sync_wait
        self.poll_cap = poll_cap
        self.output_cap = output_cap
        self.grep_line_cap = grep_line_cap
        self.glob_cap = glob_cap
        # 每次评测一个独立状态目录,避免 trial 间串扰
        self.state_dir = state_dir or f"/tmp/.wl-eval-{uuid.uuid4().hex[:8]}"
        self._commands: dict[str, str] = {}  # command_id -> 原始命令
        self._last_command_id: str = ""
        self._has_base64: Optional[bool] = None
        self._has_python3: Optional[bool] = None
        self._handlers = {
            "run_shell_command": self._run_shell,
            "read_files": self._read_files,
            "grep": self._grep,
            "file_glob": self._file_glob,
            "file_glob_v2": self._file_glob_v2,
            "search_codebase": self._search_codebase,
            "apply_file_diffs": self._apply_file_diffs,
            "read_shell_command_output": self._read_shell_output,
            "transfer_shell_command_control_to_user": self._transfer_control,
        }

    # ---- 基础设施 ----

    async def _exec(self, command: str, timeout: int = 120, cwd: Optional[str] = None):
        """在容器工作目录下执行 shell,统一异常为 return_code=124 的结果。"""
        try:
            return await self._env.exec(
                command, cwd=cwd or self.workdir, timeout_sec=timeout
            )
        except asyncio.TimeoutError:
            raise
        except Exception as exc:  # docker 层错误等
            log.warning("environment.exec 失败: %s", exc)

            class _Err:
                stdout = ""
                stderr = f"environment.exec failed: {exc}"
                return_code = 124

            return _Err()

    @staticmethod
    def _norm(out) -> tuple[str, str, int]:
        return out.stdout or "", out.stderr or "", out.return_code

    def _cap(self, text: str) -> str:
        """截断超长输出:保留头 2000 字符 + 尾部,中间标记。"""
        if len(text) <= self.output_cap:
            return text
        head = 2000
        tail = self.output_cap - head
        return (
            text[:head]
            + f"\n...[{len(text) - self.output_cap} chars truncated]...\n"
            + text[-tail:]
        )

    async def _detect_tools(self) -> None:
        if self._has_base64 is not None:
            return
        out = await self._exec(
            "command -v base64 || true; command -v python3 || true", timeout=15
        )
        stdout, _, _ = self._norm(out)
        self._has_base64 = "base64" in stdout
        self._has_python3 = "python3" in stdout
        log.info(
            "容器工具探测: base64=%s python3=%s", self._has_base64, self._has_python3
        )

    # ---- 长命令包装 ----

    def _launch_script(self, cid: str, command: str) -> str:
        log_file = shlex.quote(f"{self.state_dir}/{cid}.log")
        exit_file = shlex.quote(f"{self.state_dir}/{cid}.exit")
        inner = f"( {command} ) > {log_file} 2>&1; echo $? > {exit_file}"
        # 注意必须用 ';' 而不是 '&&' 连接:'A && B & C' 会把整个 A && B 链一起
        # 放进后台子 shell,它会占住 stdout 管道直到命令跑完,launch 就不再即时返回。
        return (
            f"mkdir -p {shlex.quote(self.state_dir)}; "
            f"nohup sh -c {shlex.quote(inner)} >/dev/null 2>&1 & "
            f"echo __WL_LAUNCHED__"
        )

    def _wait_script(self, cid: str, seconds: int) -> str:
        base = shlex.quote(f"{self.state_dir}/{cid}")
        cap = self.output_cap
        # 轮询 exit 标记;输出状态行 + 截断后的日志。全部 POSIX,busybox 可跑。
        return (
            f'E="{base}.exit"; L="{base}.log"; '
            f"i=0; while [ ! -f \"$E\" ] && [ \"$i\" -lt {seconds} ]; do sleep 1; i=$((i+1)); done; "
            f'if [ -f "$E" ]; then S=DONE; else S=RUNNING; fi; '
            f'C=$(wc -c < "$L" 2>/dev/null); C=${{C:-0}}; C=$(echo $C); '
            f'printf \'__WL_STATUS__=%s\\n\' "$S"; '
            f'printf \'__WL_EXIT__=%s\\n\' "$(cat "$E" 2>/dev/null)"; '
            f"echo '__WL_LOG__'; "
            f'if [ "$C" -gt {cap} ] 2>/dev/null; then '
            f'head -c 2000 "$L"; printf \'\\n...[truncated]...\\n\'; tail -c {cap - 2000} "$L"; '
            f'else cat "$L" 2>/dev/null; fi'
        )

    async def _wait(self, cid: str, seconds: int) -> tuple[bool, int, str]:
        """等待命令结束。返回 (是否结束, exit_code, 截断后的日志)。"""
        out = await self._exec(self._wait_script(cid, seconds), timeout=seconds + 60)
        stdout, _, _ = self._norm(out)
        done = "__WL_STATUS__=DONE" in stdout
        exit_code = -1
        m = re.search(r"__WL_EXIT__=(-?\d+)", stdout)
        if done and m:
            exit_code = int(m.group(1))
        elif done:
            exit_code = 0
        _, _, log_text = stdout.partition("__WL_LOG__\n")
        if not log_text and "__WL_LOG__" in stdout:
            log_text = stdout.split("__WL_LOG__", 1)[1].lstrip("\n")
        return done, exit_code, log_text

    # ---- 工具分发 ----

    async def execute(
        self, tool_call
    ) -> request_pb2.Request.Input.ToolCallResult:
        """执行一个 ToolCallMsg,返回可回传的 ToolCallResult。工具级失败不抛出。"""
        tr = request_pb2.Request.Input.ToolCallResult()
        tr.tool_call_id = tool_call.tool_call_id
        handler = self._handlers.get(tool_call.kind)
        if handler is None:
            raise ValueError(f"不支持的工具: {tool_call.kind}")
        try:
            await handler(tool_call.payload, getattr(tr, tool_call.kind))
        except Exception as exc:  # 兜底:任何意外都要回传结果保持配对
            log.exception("工具 %s 执行异常", tool_call.kind)
            self._fill_unexpected_error(tr, tool_call.kind, exc)
        return tr

    @staticmethod
    def _fill_unexpected_error(tr, kind: str, exc: Exception) -> None:
        msg = f"executor error: {exc}"
        slot = getattr(tr, kind)
        if kind == "run_shell_command":
            slot.command_finished.output = msg
            slot.command_finished.exit_code = 127
        elif kind in ("read_shell_command_output", "transfer_shell_command_control_to_user"):
            slot.command_finished.output = msg
            slot.command_finished.exit_code = 127
        else:
            slot.error.message = msg

    # ---- run_shell_command / 长命令族 ----

    async def _run_shell(self, payload, res) -> None:
        command = payload.command
        cid = uuid.uuid4().hex[:12]
        res.command = command
        out = await self._exec(self._launch_script(cid, command), timeout=30)
        _, stderr, rc = self._norm(out)
        if rc != 0:
            res.command_finished.output = f"failed to launch command: {stderr}"
            res.command_finished.exit_code = 127
            return
        self._commands[cid] = command
        self._last_command_id = cid
        # adapter 端会丢弃 wait_until_complete 字段;防御性尊重"不等待"语义
        wait = self.sync_wait
        if payload.HasField("wait_until_complete") and not payload.wait_until_complete:
            wait = 0
        done, exit_code, log_text = await self._wait(cid, wait)
        if done:
            res.command_finished.output = log_text
            res.command_finished.exit_code = exit_code
            res.command_finished.command_id = cid
        else:
            res.long_running_command_snapshot.output = log_text
            res.long_running_command_snapshot.command_id = cid

    async def _read_shell_output(self, payload, res) -> None:
        cid = payload.command_id
        res.command = self._commands.get(cid, "")
        if cid not in self._commands:
            res.error.command_not_found.CopyFrom(empty_pb2.Empty())
            return
        delay = payload.WhichOneof("delay")
        if delay == "duration":
            seconds = min(int(payload.duration.seconds), self.poll_cap)
        else:  # on_completion 或未指定:封顶等待,命中封顶时标 is_preempted
            seconds = self.poll_cap
        done, exit_code, log_text = await self._wait(cid, seconds)
        if done:
            res.command_finished.output = log_text
            res.command_finished.exit_code = exit_code
            res.command_finished.command_id = cid
        else:
            res.long_running_command_snapshot.output = log_text
            res.long_running_command_snapshot.command_id = cid
            res.long_running_command_snapshot.is_preempted = True

    async def _transfer_control(self, payload, res) -> None:
        cid = self._last_command_id
        if not cid or cid not in self._commands:
            res.error.command_not_found.CopyFrom(empty_pb2.Empty())
            return
        done, exit_code, log_text = await self._wait(cid, 0)
        if done:
            res.command_finished.output = log_text
            res.command_finished.exit_code = exit_code
            res.command_finished.command_id = cid
        else:
            res.long_running_command_snapshot.output = (
                NO_USER_TERMINAL_NOTE + "\n" + log_text
            )
            res.long_running_command_snapshot.command_id = cid

    # ---- read_files ----

    async def _read_files(self, payload, res) -> None:
        contents: list[file_content_pb2.FileContent] = []
        errors: list[str] = []
        for f in payload.files:
            path = f.name
            if f.line_ranges:
                for lr in f.line_ranges:
                    out = await self._exec(
                        f"sed -n '{lr.start},{lr.end}p' {shlex.quote(path)}"
                    )
                    stdout, stderr, rc = self._norm(out)
                    if rc != 0:
                        errors.append(f"{path}: {stderr.strip() or 'read failed'}")
                        continue
                    fc = file_content_pb2.FileContent()
                    fc.file_path = path
                    fc.content = self._cap(stdout)
                    fc.line_range.start = lr.start
                    fc.line_range.end = lr.end
                    contents.append(fc)
            else:
                cap1 = self.output_cap + 1
                out = await self._exec(
                    f"if [ -d {shlex.quote(path)} ]; then exit 4; "
                    f"elif [ -f {shlex.quote(path)} ]; then head -c {cap1} {shlex.quote(path)}; "
                    f"else exit 3; fi"
                )
                stdout, _, rc = self._norm(out)
                if rc == 3:
                    errors.append(f"{path}: no such file")
                    continue
                if rc == 4:
                    errors.append(f"{path}: is a directory")
                    continue
                if rc != 0:
                    errors.append(f"{path}: read failed (rc={rc})")
                    continue
                fc = file_content_pb2.FileContent()
                fc.file_path = path
                fc.content = self._cap(stdout)
                contents.append(fc)
        if contents:
            for err in errors:  # 部分失败:以内联文本告知模型
                fc = file_content_pb2.FileContent()
                fc.file_path = "(error)"
                fc.content = f"<error: {err}>"
                contents.append(fc)
            res.text_files_success.files.extend(contents)
        elif errors:
            res.error.message = "; ".join(errors)
        else:
            res.error.message = "no files requested"

    # ---- grep / glob / search ----

    async def _grep(self, payload, res) -> None:
        target = payload.path or "."
        patterns = list(payload.queries)
        if not patterns:
            res.error.message = "no query given"
            return
        pats = " ".join("-e " + shlex.quote(p) for p in patterns)
        cmd = (
            f"grep -rEnI {pats} -- {shlex.quote(target)} "
            f"| head -n {self.grep_line_cap}"
        )
        out = await self._exec(cmd)
        stdout, stderr, _ = self._norm(out)
        matches: dict[str, list[int]] = {}
        for line in stdout.splitlines():
            m = _LINE_RE.match(line)
            if not m:
                continue
            path, lineno = m.group(1), int(m.group(2))
            entry = matches.setdefault(path, [])
            if lineno not in entry:
                entry.append(lineno)
        if not matches and stderr.strip():
            res.error.message = stderr.strip()[:2000]
            return
        for path, linenos in matches.items():
            fm = res.success.matched_files.add()
            fm.file_path = path
            for n in sorted(linenos):
                fm.matched_lines.add(line_number=n)

    @staticmethod
    def _find_clause(patterns) -> str:
        parts = []
        for p in patterns:
            if p.startswith("**/"):  # '**/*.py' → 递归按文件名匹配
                parts.append("-name " + shlex.quote(p[3:]))
            elif "/" in p:
                parts.append("-path " + shlex.quote("*/" + p))
            else:
                parts.append("-name " + shlex.quote(p))
        if not parts:
            return ""
        return "\\( " + " -o ".join(parts) + " \\)"

    async def _file_glob(self, payload, res) -> None:
        target = payload.path or "."
        clause = self._find_clause(list(payload.patterns))
        if not clause:
            res.error.message = "no pattern given"
            return
        out = await self._exec(
            f"find {shlex.quote(target)} -type f {clause} | head -n {self.glob_cap}"
        )
        stdout, stderr, _ = self._norm(out)
        lines = [l for l in stdout.splitlines() if l.strip()]
        if not lines and stderr.strip():
            res.error.message = stderr.strip()[:2000]
            return
        res.success.matched_files = "\n".join(lines)

    async def _file_glob_v2(self, payload, res) -> None:
        search_dir = payload.search_dir or "."
        clause = self._find_clause(list(payload.patterns))
        if not clause:
            res.error.message = "no pattern given"
            return
        depth = ""
        if payload.min_depth > 0:
            depth += f" -mindepth {payload.min_depth}"
        if payload.max_depth > 0:
            depth += f" -maxdepth {payload.max_depth}"
        head = f" | head -n {payload.max_matches}" if payload.max_matches > 0 else ""
        out = await self._exec(
            f"find {shlex.quote(search_dir)}{depth} {clause}{head}"
        )
        stdout, stderr, _ = self._norm(out)
        lines = [l for l in stdout.splitlines() if l.strip()]
        if not lines and stderr.strip():
            res.error.message = stderr.strip()[:2000]
            return
        for line in lines:
            res.success.matched_files.add(file_path=line)
        if stderr.strip():
            res.success.warnings = stderr.strip()[:2000]

    async def _search_codebase(self, payload, res) -> None:
        # 容器里没有语义索引:退化为 grep 近似(先整串、后按词 OR),诚实记录。
        base = payload.codebase_path or "."
        targets = (
            [posixpath.join(base, pf) for pf in payload.path_filters]
            if payload.path_filters
            else [base]
        )
        tgt = " ".join(shlex.quote(t) for t in targets)
        query = payload.query.strip()
        if not query:
            res.error.message = "empty query"
            return
        stdout, stderr = await self._grep_raw(query, tgt)
        if not stdout.strip() and " " in query:
            words = query.split()[:6]
            pats = " ".join("-e " + shlex.quote(w) for w in words)
            stdout, stderr = await self._grep_raw(pats, tgt, raw_patterns=True)
        if not stdout.strip() and stderr.strip():
            res.error.message = stderr.strip()[:2000]
            return
        files: dict[str, list[tuple[int, str]]] = {}
        for line in stdout.splitlines():
            m = _LINE_RE.match(line)
            if not m:
                continue
            entry = files.setdefault(m.group(1), [])
            if len(entry) < 20:
                entry.append((int(m.group(2)), m.group(3)))
        for path, hits in list(files.items())[:8]:
            fc = file_content_pb2.FileContent()
            fc.file_path = path
            fc.content = "\n".join(f"{n}: {t}" for n, t in hits)
            res.success.files.append(fc)

    async def _grep_raw(self, pattern_or_query: str, tgt: str, raw_patterns: bool = False):
        pats = pattern_or_query if raw_patterns else "-e " + shlex.quote(pattern_or_query)
        out = await self._exec(f"grep -rEniI {pats} -- {tgt} | head -n 200")
        stdout, stderr, _ = self._norm(out)
        return stdout, stderr

    # ---- apply_file_diffs ----

    async def _read_bytes(self, path: str) -> tuple[Optional[bytes], Optional[str]]:
        await self._detect_tools()
        if self._has_base64:
            # stdin 重定向兼容 GNU/busybox/macOS(后两者不认位置参数文件名)
            cmd = f"base64 < {shlex.quote(path)} | tr -d '\\n'"
        elif self._has_python3:
            cmd = (
                "python3 -c \"import base64,sys;"
                "sys.stdout.write(base64.b64encode(open(sys.argv[1],'rb').read()).decode())\" "
                + shlex.quote(path)
            )
        else:
            return None, "container has neither base64 nor python3"
        out = await self._exec(cmd)
        stdout, stderr, rc = self._norm(out)
        if rc != 0:
            return None, (stderr.strip() or f"read failed (rc={rc})")
        try:
            return base64.b64decode(stdout), None
        except Exception as exc:
            return None, f"base64 decode failed: {exc}"

    async def _write_bytes(self, path: str, data: bytes) -> Optional[str]:
        """分块 base64 写回容器。返回 None 表示成功,否则为错误信息。"""
        await self._detect_tools()
        if not self._has_base64 and not self._has_python3:
            return "container has neither base64 nor python3"
        b64 = base64.b64encode(data).decode()
        tmp = f"{self.state_dir}/w-{uuid.uuid4().hex[:8]}.b64"
        parent = posixpath.dirname(path) or "."
        out = await self._exec(
            f"mkdir -p {shlex.quote(self.state_dir)} {shlex.quote(parent)}"
        )
        _, stderr, rc = self._norm(out)
        if rc != 0:
            return f"mkdir failed: {stderr.strip()}"
        qtmp = shlex.quote(tmp)
        chunks = [b64[i : i + 48000] for i in range(0, len(b64), 48000)] or [""]
        for i, chunk in enumerate(chunks):
            op = ">" if i == 0 else ">>"
            out = await self._exec(f"printf '%s' '{chunk}' {op} {qtmp}", timeout=60)
            _, stderr, rc = self._norm(out)
            if rc != 0:
                return f"write chunk failed: {stderr.strip()}"
        if self._has_base64:
            decode_cmd = f"base64 -d < {qtmp} > {shlex.quote(path)}"
        else:
            decode_cmd = (
                "python3 -c \"import base64,sys;"
                "open(sys.argv[2],'wb').write(base64.b64decode(open(sys.argv[1],'rb').read()))\" "
                f"{qtmp} {shlex.quote(path)}"
            )
        out = await self._exec(f"{decode_cmd}; rc=$?; rm -f {qtmp}; exit $rc")
        _, stderr, rc = self._norm(out)
        if rc != 0:
            return f"decode/write failed: {stderr.strip()}"
        return None

    async def _apply_file_diffs(self, payload, res) -> None:
        updated: list[tuple[str, bytes]] = []
        deleted: list[str] = []
        failures: list[str] = []
        for d in payload.diffs:
            content, err = await self._read_bytes(d.file_path)
            if err is not None and d.search != "":
                failures.append(f"{d.file_path}: {err}")
                continue
            if d.search == "":
                # 空 search = 新建/整体覆盖(与 Warp 客户端语义一致)
                new = d.replace.encode()
            else:
                needle = d.search.encode()
                if needle not in content:
                    failures.append(f"{d.file_path}: search block not found")
                    continue
                new = content.replace(needle, d.replace.encode(), 1)
            werr = await self._write_bytes(d.file_path, new)
            if werr:
                failures.append(f"{d.file_path}: {werr}")
            else:
                updated.append((d.file_path, new))
        for nf in payload.new_files:
            data = nf.content.encode()
            werr = await self._write_bytes(nf.file_path, data)
            if werr:
                failures.append(f"{nf.file_path}: {werr}")
            else:
                updated.append((nf.file_path, data))
        for df in payload.deleted_files:
            out = await self._exec(f"rm -f {shlex.quote(df.file_path)}")
            _, stderr, rc = self._norm(out)
            if rc != 0:
                failures.append(f"{df.file_path}: delete failed: {stderr.strip()}")
            else:
                deleted.append(df.file_path)
        if failures:
            # per-file 成功失败都要报给模型
            lines = [f"OK {p}" for p, _ in updated]
            lines += [f"deleted {p}" for p in deleted]
            lines += [f"FAIL {e}" for e in failures]
            res.error.message = "\n".join(lines)
            return
        for path, data in updated:
            ufc = res.success.updated_files_v2.add()
            ufc.file.file_path = path
            ufc.file.content = data.decode("utf-8", "replace")
        for path in deleted:
            res.success.deleted_files.add(file_path=path)
