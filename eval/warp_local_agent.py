"""Harbor 外部 agent:把 Terminal-Bench 任务接到本机 warp-local-adapter。

用法:
    PYTHONPATH="$PWD" harbor run -d terminal-bench/terminal-bench-2 \
        -a eval.warp_local_agent:WarpLocalAgent -l 5

agent 进程运行在宿主机(Harbor 侧),通过 HTTP 连本机的 adapter
(默认 http://127.0.0.1:18889,可用 WARP_LOCAL_ADAPTER_BASE_URL 或
--ak adapter_base_url=... 覆盖),工具调用经 ContainerExecutor 落到
Harbor 的任务容器里执行。

产物(logs_dir 即 trial 的 agent/ 目录):
- warp-local-events.jsonl:增量事件轨迹,每轮追加,超时/异常/Ctrl-C 不丢已写内容
- trajectory.json:Harbor ATIF 轨迹(harbor view 读取点),正常结束与
  超时 cancel 路径都会尽力写出
- warp-local-transcript.json / warp-local-final.txt:原有兼容产物
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import time
from pathlib import Path

# 让 tb_client 在 harbor 的解释器里可导入(本文件所在目录即 tb_client 的父目录)
sys.path.insert(0, str(Path(__file__).resolve().parent))

from harbor.agents.base import BaseAgent  # noqa: E402
from harbor.environments.base import BaseEnvironment  # noqa: E402
from harbor.models.agent.context import AgentContext  # noqa: E402

from tb_client.client import AdapterClient  # noqa: E402
from tb_client.events import EventLogger  # noqa: E402
from tb_client.executor import ContainerExecutor  # noqa: E402
from tb_client.trajectory import (  # noqa: E402
    RoundRecord,
    ToolExecRecord,
    build_atif,
    pb_payload_args,
    pb_result_text,
    utc_now_iso,
)

DEFAULT_BASE_URL = "http://127.0.0.1:18889"


class WarpLocalAgent(BaseAgent):
    """Terminal-Bench × warp-local-adapter 桥接 agent。"""

    # 本 agent 产出 ATIF 轨迹(trajectory.json)
    SUPPORTS_ATIF: bool = True

    def __init__(self, *args, adapter_base_url: str = "", adapter_base_urls: str = "", max_rounds: int = 0, **kwargs):
        super().__init__(*args, **kwargs)
        # 多 adapter 池(每 adapter 独立记忆目录,实现按题隔离的记忆评测):
        # WARP_LOCAL_ADAPTER_BASE_URLS="http://127.0.0.1:18911,http://..." 优先;
        # 否则退回单地址 WARP_LOCAL_ADAPTER_BASE_URL / 默认值。
        urls = (
            adapter_base_urls
            or os.environ.get("WARP_LOCAL_ADAPTER_BASE_URLS", "")
        )
        self._base_urls = [u.strip().rstrip("/") for u in urls.split(",") if u.strip()]
        self._base_url = (
            adapter_base_url
            or os.environ.get("WARP_LOCAL_ADAPTER_BASE_URL")
            or (self._base_urls[0] if self._base_urls else "")
            or DEFAULT_BASE_URL
        ).rstrip("/")
        self._max_rounds = max_rounds or int(
            os.environ.get("WARP_LOCAL_MAX_ROUNDS", "100")
        )
        self._cmd_timeout = int(os.environ.get("WARP_LOCAL_CMD_TIMEOUT", "120"))
        self._output_cap = int(os.environ.get("WARP_LOCAL_OUTPUT_CAP", "60000"))

    @staticmethod
    def name() -> str:
        return "warp-local-adapter"

    def version(self) -> str:
        return "0.2.0"

    async def setup(self, environment: BaseEnvironment) -> None:
        """起评前探活 adapter;失败只告警,run 里再硬失败(便于拿到错误上下文)。"""
        import urllib.request

        def _probe() -> str:
            with urllib.request.urlopen(
                f"{self._base_url}/health", timeout=5
            ) as resp:
                return resp.read().decode(errors="replace")

        try:
            status = await asyncio.to_thread(_probe)
            self.logger.info("adapter /health: %s", status)
        except Exception as exc:
            self.logger.warning(
                "adapter %s 不可达(%s);请先按 eval/README.md 启动 adapter",
                self._base_url,
                exc,
            )

    async def _detect_workdir(self, environment: BaseEnvironment) -> str:
        task_cfg = getattr(environment, "task_env_config", None)
        workdir = getattr(task_cfg, "workdir", None)
        if workdir:
            return str(workdir)
        result = await environment.exec("pwd", timeout_sec=15)
        return (result.stdout or "/").strip() or "/"

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        # 多 adapter 池:按题面前缀哈希选实例,保证同一题始终落到同一
        # adapter(=同一独立记忆目录),不同题均匀散开。
        if self._base_urls:
            import hashlib

            idx = int(hashlib.md5(instruction[:128].encode()).hexdigest(), 16) % len(
                self._base_urls
            )
            self._base_url = self._base_urls[idx]
        workdir = await self._detect_workdir(environment)
        os_name = str(getattr(environment, "os", "linux")).lower()
        os_platform = "Windows" if "windows" in os_name else "Linux"
        self.logger.info(
            "warp-local agent 开始: adapter=%s workdir=%s", self._base_url, workdir
        )

        Path(self.logs_dir).mkdir(parents=True, exist_ok=True)
        events = EventLogger(Path(self.logs_dir) / "warp-local-events.jsonl")
        client = AdapterClient(
            self._base_url,
            workdir,
            os_platform=os_platform,
            shell_name="bash",
            event_logger=events,
        )
        executor = ContainerExecutor(
            environment,
            workdir,
            sync_wait=self._cmd_timeout,
            output_cap=self._output_cap,
        )

        records: list[RoundRecord] = []
        tool_calls = 0
        errors: list[str] = []
        finish = ""
        final_text = ""
        started = time.time()

        try:
            events.log(
                "instruction",
                instruction=instruction,
                workdir=workdir,
                adapter=self._base_url,
            )
            t0 = time.monotonic()
            rnd = await asyncio.to_thread(client.start, instruction)
            while True:
                rec = RoundRecord(
                    round_no=len(records) + 1,
                    ts=utc_now_iso(),
                    text=rnd.full_text,
                    finish=rnd.finish,
                    error=rnd.error,
                    duration_sec=time.monotonic() - t0,
                )
                records.append(rec)
                if rnd.error:
                    errors.append(f"round {rec.round_no}: {rnd.error}")
                    break
                finish = rnd.finish
                if not rnd.tool_calls:
                    final_text = rnd.full_text
                    break
                if rec.round_no >= self._max_rounds:
                    errors.append(f"达到最大轮数 {self._max_rounds}")
                    break
                results = []
                for tc in rnd.tool_calls:
                    tool_calls += 1
                    te = ToolExecRecord(
                        tool_call_id=tc.tool_call_id,
                        kind=tc.kind,
                        args=pb_payload_args(tc.payload),
                    )
                    rec.tool_execs.append(te)
                    te_t0 = time.monotonic()
                    try:
                        tr = await executor.execute(tc)
                        te.result = pb_result_text(getattr(tr, tc.kind))
                    except Exception as exc:  # 协议层不认识的工具,无法回传配对结果
                        te.error = str(exc)
                        te.duration_sec = time.monotonic() - te_t0
                        errors.append(f"工具 {tc.kind} 无法回传结果: {exc}")
                        break
                    te.duration_sec = time.monotonic() - te_t0
                    events.log(
                        "tool_result",
                        rec.round_no,
                        tool_call_id=tc.tool_call_id,
                        kind=tc.kind,
                        result=te.result,
                        duration_sec=round(te.duration_sec, 3),
                    )
                    results.append(tr)
                else:
                    t0 = time.monotonic()
                    rnd = await asyncio.to_thread(client.send_tool_results, results)
                    continue
                break  # 内层 break(工具失败)→ 终止
        except asyncio.CancelledError:
            # harbor 用 asyncio.wait_for 限时,超时即 cancel 当前协程:
            # 尽力落盘(增量 JSONL 已在逐轮写,这里补 ATIF 轨迹与上下文)再抛出
            errors.append("run cancelled(harbor 超时或外部中断)")
            events.log("cancelled", errors=errors[-1])
            self._write_artifacts(
                events, client, instruction, records, errors, finish, final_text, started
            )
            self._fill_context(context, records, tool_calls, errors, finish, final_text, client, workdir)
            raise
        except Exception as exc:
            self.logger.exception("agent 运行异常")
            errors.append(f"{type(exc).__name__}: {exc}")
            events.log("error", error=errors[-1])

        self._write_artifacts(
            events, client, instruction, records, errors, finish, final_text, started
        )
        self._fill_context(context, records, tool_calls, errors, finish, final_text, client, workdir)

    # ---- 产物落盘 ----

    def _write_artifacts(
        self,
        events: EventLogger,
        client: AdapterClient,
        instruction: str,
        records: list[RoundRecord],
        errors: list[str],
        finish: str,
        final_text: str,
        started: float,
    ) -> None:
        """写全部轨迹产物;任何单项失败只告警,不阻断其他产物。"""
        summary = {
            "rounds": len(records),
            "tool_calls": sum(len(r.tool_execs) for r in records),
            "errors": errors,
            "finish": finish,
            "elapsed_sec": round(time.time() - started, 1),
            "conversation_id": client.conversation_id,
        }
        try:
            events.log("summary", **summary)
            events.close()
        except Exception as exc:
            self.logger.warning("关闭事件日志失败: %s", exc)
        try:
            transcript = [
                {
                    "round": r.round_no,
                    "text": r.text,
                    "tool_calls": [
                        {"id": te.tool_call_id, "kind": te.kind} for te in r.tool_execs
                    ],
                    "finish": r.finish,
                    "error": r.error,
                }
                for r in records
            ] + [{"summary": summary}]
            (Path(self.logs_dir) / "warp-local-transcript.json").write_text(
                json.dumps(transcript, ensure_ascii=False, indent=2)
            )
            (Path(self.logs_dir) / "warp-local-final.txt").write_text(final_text)
        except Exception as exc:
            self.logger.warning("写 transcript 失败: %s", exc)
        try:
            trajectory = build_atif(
                instruction,
                records,
                agent_name=self.name(),
                agent_version=self.version(),
                session_id=client.conversation_id or client.task_id,
                notes=(
                    "warp-local-adapter eval agent。adapter 的 StreamFinished 不带 "
                    "token_usage(Go 端 finishEvent 未填充),token 指标留空。"
                ),
                extra={"adapter": self._base_url, "summary": summary},
            )
            self._soft_validate_atif(trajectory)
            (Path(self.logs_dir) / "trajectory.json").write_text(
                json.dumps(trajectory, ensure_ascii=False, indent=2)
            )
        except Exception as exc:
            self.logger.warning("写 trajectory.json 失败: %s", exc)

    def _soft_validate_atif(self, trajectory: dict) -> None:
        """用 harbor 自带模型做结构校验;失败只告警,轨迹照写(部分数据好过没有)。"""
        try:
            from harbor.models.trajectories.trajectory import Trajectory

            Trajectory.model_validate(trajectory)
        except Exception as exc:
            self.logger.warning("trajectory.json 未通过 ATIF 校验: %s", exc)

    def _fill_context(
        self,
        context: AgentContext,
        records: list[RoundRecord],
        tool_calls: int,
        errors: list[str],
        finish: str,
        final_text: str,
        client: AdapterClient,
        workdir: str,
    ) -> None:
        # token 指标:adapter 的 StreamFinished 不带 usage,无法填
        # n_input_tokens / n_output_tokens / cost_usd,保持 None。
        context.metadata = {
            "rounds": len(records),
            "tool_calls": tool_calls,
            "errors": errors,
            "finish": finish,
            "final_text": final_text[-4000:],
            "conversation_id": client.conversation_id,
            "adapter": self._base_url,
            "workdir": workdir,
        }
