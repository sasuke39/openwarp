"""无网络 smoke 单测:pb 消息构造、SSE 帧解析、执行器对本地沙箱的全工具链路。

运行(在 eval/ 目录、已生成 pb stub 的 venv 里):
    python3 -m unittest discover -s tb_client.tests -v
"""

from __future__ import annotations

import asyncio
import base64
import os
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # eval/ 上 sys.path

from google.protobuf import empty_pb2

from tb_client import client as client_mod
from tb_client.client import AdapterClient, ToolCallMsg, iter_sse_payloads
from tb_client.executor import ContainerExecutor
from tb_client.local_env import LocalSandbox
from tb_client.pb import response_pb2, task_pb2


def _sse_lines(events) -> list[str]:
    """把一组 ResponseEvent 编码成服务端同款 SSE 行(base64.URLEncoding)。"""
    lines: list[str] = []
    for ev in events:
        payload = base64.urlsafe_b64encode(ev.SerializeToString()).decode()
        lines += [f"data: {payload}", ""]
    return lines


class _FakeResp:
    def __init__(self, lines):
        self._lines = lines

    def iter_lines(self, decode_unicode=False):
        return iter(self._lines)


class RequestBuildTest(unittest.TestCase):
    def test_first_request_roundtrip(self):
        c = AdapterClient("http://127.0.0.1:18889", "/app", task_id="tb-test01")
        req = c.build_first_request("create hello.txt")
        self.assertEqual(req.metadata.conversation_id, "")
        self.assertEqual(req.task_context.tasks[0].id, "tb-test01")
        self.assertEqual(req.input.context.directory.pwd, "/app")
        self.assertEqual(req.input.context.operating_system.platform, "Linux")
        self.assertEqual(
            req.input.user_inputs.inputs[0].user_query.query, "create hello.txt"
        )
        # 二进制往返(服务端 proto.Unmarshal 的入参)
        raw = req.SerializeToString()
        from tb_client.pb import request_pb2

        back = request_pb2.Request()
        back.ParseFromString(raw)
        self.assertEqual(back.task_context.tasks[0].id, "tb-test01")

    def test_tool_results_request(self):
        c = AdapterClient("http://127.0.0.1:18889", "/app", task_id="tb-test02")
        c.conversation_id = "conv-1"
        from tb_client.pb import request_pb2

        tr = request_pb2.Request.Input.ToolCallResult()
        tr.tool_call_id = "call-1"
        tr.run_shell_command.command = "ls"
        tr.run_shell_command.command_finished.output = "ok"
        tr.run_shell_command.command_finished.exit_code = 0
        req = c.build_tool_results_request([tr])
        self.assertEqual(req.metadata.conversation_id, "conv-1")
        got = req.input.user_inputs.inputs[0].tool_call_result
        self.assertEqual(got.tool_call_id, "call-1")
        self.assertEqual(got.run_shell_command.command_finished.output, "ok")


class SseParseTest(unittest.TestCase):
    def test_full_round_events(self):
        init = response_pb2.ResponseEvent()
        init.init.conversation_id = "conv-9"
        init.init.request_id = "req-9"

        add = response_pb2.ResponseEvent()
        msg = add.client_actions.actions.add().add_messages_to_task.messages.add()
        msg.id = "m1"
        msg.agent_output.text = "你好"

        append = response_pb2.ResponseEvent()
        amsg = append.client_actions.actions.add().append_to_message_content.message
        amsg.id = "m1"
        amsg.agent_output.text = "世界"

        tc_add = response_pb2.ResponseEvent()
        tmsg = tc_add.client_actions.actions.add().add_messages_to_task.messages.add()
        tmsg.id = "m2"
        tmsg.tool_call.tool_call_id = "call-7"
        tmsg.tool_call.run_shell_command.command = "ls -la"

        fin = response_pb2.ResponseEvent()
        fin.finished.done.CopyFrom(response_pb2.ResponseEvent.StreamFinished.Done())

        c = AdapterClient("http://127.0.0.1:18889", "/app", task_id="tb-t03")
        rnd = c._consume(_FakeResp(_sse_lines([init, add, append, tc_add, fin])))
        self.assertEqual(rnd.conversation_id, "conv-9")
        self.assertEqual(rnd.full_text, "你好世界")
        self.assertEqual(len(rnd.tool_calls), 1)
        self.assertEqual(rnd.tool_calls[0].kind, "run_shell_command")
        self.assertEqual(rnd.tool_calls[0].payload.command, "ls -la")
        self.assertEqual(rnd.finish, "done")
        self.assertIsNone(rnd.error)

    def test_internal_error(self):
        fin = response_pb2.ResponseEvent()
        fin.finished.internal_error.message = "LLM boom"
        c = AdapterClient("http://127.0.0.1:18889", "/app")
        rnd = c._consume(_FakeResp(_sse_lines([fin])))
        self.assertEqual(rnd.finish, "internal_error")
        self.assertEqual(rnd.error, "LLM boom")

    def test_multiline_data_and_padding(self):
        ev = response_pb2.ResponseEvent()
        ev.finished.done.CopyFrom(response_pb2.ResponseEvent.StreamFinished.Done())
        payload = base64.urlsafe_b64encode(ev.SerializeToString()).decode()
        # 去掉 padding、拆成两行 data
        payload = payload.rstrip("=")
        lines = [f"data: {payload[:3]}", f"data: {payload[3:]}", ""]
        got = list(iter_sse_payloads(lines))
        self.assertEqual(len(got), 1)
        parsed = response_pb2.ResponseEvent()
        parsed.ParseFromString(client_mod._b64url_decode(got[0]))
        self.assertEqual(parsed.WhichOneof("type"), "finished")


class ExecutorTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="wl-exec-test-")
        self.workdir = self._tmp.name
        self.env = LocalSandbox(self.workdir)
        self.ex = ContainerExecutor(
            self.env, self.workdir, sync_wait=1, poll_cap=20, output_cap=4000
        )

    async def asyncTearDown(self):
        self._tmp.cleanup()

    def _tc(self, kind: str, payload) -> ToolCallMsg:
        return ToolCallMsg(tool_call_id="call-" + kind, kind=kind, payload=payload)

    async def test_run_shell_command_finished(self):
        p = task_pb2.Message.ToolCall.RunShellCommand(command="echo hi; pwd")
        tr = await self.ex.execute(self._tc("run_shell_command", p))
        self.assertEqual(tr.tool_call_id, "call-run_shell_command")
        fin = tr.run_shell_command.command_finished
        self.assertEqual(fin.exit_code, 0)
        self.assertIn("hi", fin.output)
        self.assertEqual(tr.run_shell_command.command, "echo hi; pwd")

    async def test_long_running_then_read_output(self):
        p = task_pb2.Message.ToolCall.RunShellCommand(command="sleep 3; echo woke")
        tr = await self.ex.execute(self._tc("run_shell_command", p))
        snap = tr.run_shell_command.long_running_command_snapshot
        self.assertNotEqual(snap.command_id, "")
        cid = snap.command_id

        ro = task_pb2.Message.ToolCall.ReadShellCommandOutput(command_id=cid)
        ro.on_completion.CopyFrom(empty_pb2.Empty())
        tr2 = await self.ex.execute(self._tc("read_shell_command_output", ro))
        fin = tr2.read_shell_command_output.command_finished
        self.assertEqual(fin.exit_code, 0)
        self.assertIn("woke", fin.output)
        self.assertEqual(tr2.read_shell_command_output.command, "sleep 3; echo woke")

    async def test_read_unknown_command(self):
        ro = task_pb2.Message.ToolCall.ReadShellCommandOutput(command_id="nope")
        ro.on_completion.CopyFrom(empty_pb2.Empty())
        tr = await self.ex.execute(self._tc("read_shell_command_output", ro))
        self.assertEqual(tr.read_shell_command_output.WhichOneof("result"), "error")

    async def test_transfer_control(self):
        p = task_pb2.Message.ToolCall.RunShellCommand(command="sleep 5; echo zz")
        tr = await self.ex.execute(self._tc("run_shell_command", p))
        cid = tr.run_shell_command.long_running_command_snapshot.command_id
        self.assertNotEqual(cid, "")
        tf = task_pb2.Message.ToolCall.TransferShellCommandControlToUser(reason="r")
        tr2 = await self.ex.execute(
            self._tc("transfer_shell_command_control_to_user", tf)
        )
        snap = tr2.transfer_shell_command_control_to_user.long_running_command_snapshot
        self.assertEqual(snap.command_id, cid)
        self.assertIn("No interactive user terminal", snap.output)

    async def test_read_files_and_ranges(self):
        Path(self.workdir, "a.txt").write_text("l1\nl2\nl3\n")
        rf = task_pb2.Message.ToolCall.ReadFiles()
        f = rf.files.add()
        f.name = "a.txt"
        lr = f.line_ranges.add()
        lr.start = 2
        lr.end = 3
        tr = await self.ex.execute(self._tc("read_files", rf))
        files = tr.read_files.text_files_success.files
        self.assertEqual(len(files), 1)
        self.assertEqual(files[0].content, "l2\nl3\n")
        self.assertEqual(files[0].line_range.start, 2)

        rf2 = task_pb2.Message.ToolCall.ReadFiles()
        rf2.files.add().name = "missing.txt"
        tr2 = await self.ex.execute(self._tc("read_files", rf2))
        self.assertEqual(tr2.read_files.WhichOneof("result"), "error")

    async def test_grep_and_globs(self):
        Path(self.workdir, "g.py").write_text("def foo():\n    return 42\n")
        os.makedirs(Path(self.workdir, "sub"), exist_ok=True)
        Path(self.workdir, "sub", "h.py").write_text("x = 42\n")

        g = task_pb2.Message.ToolCall.Grep(queries=["return 42"], path=".")
        tr = await self.ex.execute(self._tc("grep", g))
        matched = tr.grep.success.matched_files
        self.assertEqual(len(matched), 1)
        self.assertTrue(matched[0].file_path.endswith("g.py"))
        self.assertEqual(matched[0].matched_lines[0].line_number, 2)

        fg = task_pb2.Message.ToolCall.FileGlob(patterns=["*.py"], path=".")
        tr2 = await self.ex.execute(self._tc("file_glob", fg))
        self.assertIn("g.py", tr2.file_glob.success.matched_files)

        fg2 = task_pb2.Message.ToolCall.FileGlobV2(
            patterns=["*.py"], search_dir=".", max_depth=1
        )
        tr3 = await self.ex.execute(self._tc("file_glob_v2", fg2))
        paths = [m.file_path for m in tr3.file_glob_v2.success.matched_files]
        self.assertTrue(any(p.endswith("g.py") for p in paths))
        self.assertFalse(any(p.endswith("h.py") for p in paths))  # max_depth=1 排除 sub/

    async def test_search_codebase_fallback(self):
        Path(self.workdir, "s.py").write_text("alpha beta gamma\n")
        sc = task_pb2.Message.ToolCall.SearchCodebase(query="beta gamma")
        tr = await self.ex.execute(self._tc("search_codebase", sc))
        files = tr.search_codebase.success.files
        self.assertTrue(any(f.file_path.endswith("s.py") for f in files))

    async def test_apply_file_diffs_full_cycle(self):
        afd = task_pb2.Message.ToolCall.ApplyFileDiffs(summary="t")
        afd.new_files.add(file_path="n.txt", content="hello world\n")
        tr = await self.ex.execute(self._tc("apply_file_diffs", afd))
        self.assertEqual(tr.apply_file_diffs.WhichOneof("result"), "success")
        self.assertEqual(Path(self.workdir, "n.txt").read_text(), "hello world\n")

        afd2 = task_pb2.Message.ToolCall.ApplyFileDiffs(summary="t2")
        d = afd2.diffs.add()
        d.file_path = "n.txt"
        d.search = "world"
        d.replace = "tb"
        tr2 = await self.ex.execute(self._tc("apply_file_diffs", afd2))
        self.assertEqual(tr2.apply_file_diffs.WhichOneof("result"), "success")
        self.assertEqual(Path(self.workdir, "n.txt").read_text(), "hello tb\n")

        afd3 = task_pb2.Message.ToolCall.ApplyFileDiffs(summary="t3")
        bad = afd3.diffs.add()
        bad.file_path = "n.txt"
        bad.search = "not-there"
        bad.replace = "x"
        tr3 = await self.ex.execute(self._tc("apply_file_diffs", afd3))
        self.assertEqual(tr3.apply_file_diffs.WhichOneof("result"), "error")
        self.assertIn("search block not found", tr3.apply_file_diffs.error.message)

        afd4 = task_pb2.Message.ToolCall.ApplyFileDiffs(summary="t4")
        afd4.deleted_files.add(file_path="n.txt")
        tr4 = await self.ex.execute(self._tc("apply_file_diffs", afd4))
        self.assertEqual(tr4.apply_file_diffs.WhichOneof("result"), "success")
        self.assertFalse(Path(self.workdir, "n.txt").exists())


if __name__ == "__main__":
    unittest.main()
