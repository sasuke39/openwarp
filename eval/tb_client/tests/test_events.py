"""EventLogger 与 client 事件接线的单测。"""

from __future__ import annotations

import base64
import json
import sys
import tempfile
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # eval/ 上 sys.path

from tb_client.client import AdapterClient
from tb_client.events import EventLogger
from tb_client.pb import response_pb2


def _sse_response(events):
    """伪造一个带 SSE 流的 requests.Response。"""

    class FakeResp:
        def raise_for_status(self):
            pass

        def iter_lines(self, decode_unicode=False):
            for ev in events:
                payload = base64.urlsafe_b64encode(ev.SerializeToString()).decode()
                yield f"data: {payload}"
                yield ""

        def close(self):
            pass

    return FakeResp()


class EventLoggerTest(unittest.TestCase):
    def test_jsonl_fields_and_append(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "events.jsonl"
            lg = EventLogger(path)
            lg.log("instruction", instruction="do it", workdir="/work")
            lg.log("request", 1, input_kinds=["user_query"])
            lg.log("round", 1, text="hello", finish="done")
            lg.close()
            # 重新打开应为追加而不是覆盖
            lg2 = EventLogger(path)
            lg2.log("summary", rounds=1)
            lg2.close()

            lines = path.read_text().strip().split("\n")
            self.assertEqual(len(lines), 4)
            recs = [json.loads(l) for l in lines]
            for r in recs:
                self.assertIn("ts", r)
                self.assertIn("type", r)
            self.assertEqual(recs[0]["type"], "instruction")
            self.assertEqual(recs[1]["round"], 1)
            self.assertEqual(recs[2]["text"], "hello")
            self.assertEqual(recs[3]["type"], "summary")

    def test_concurrent_writes_not_interleaved(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "events.jsonl"
            lg = EventLogger(path)

            def worker(tag: str):
                for i in range(50):
                    lg.log("tick", i, tag=tag, payload="x" * 100)

            threads = [threading.Thread(target=worker, args=(f"t{n}",)) for n in range(4)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            lg.close()
            lines = path.read_text().strip().split("\n")
            self.assertEqual(len(lines), 200)
            for l in lines:  # 每行都应是完整 JSON(无交错写坏)
                self.assertEqual(json.loads(l)["type"], "tick")

    def test_log_after_close_is_noop(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "events.jsonl"
            lg = EventLogger(path)
            lg.log("a")
            lg.close()
            lg.log("b")  # 不应抛异常也不应写入
            self.assertEqual(len(path.read_text().strip().split("\n")), 1)


class ClientEventWiringTest(unittest.TestCase):
    def test_post_logs_request_and_round(self):
        with tempfile.TemporaryDirectory() as d:
            lg = EventLogger(Path(d) / "events.jsonl")
            client = AdapterClient(
                "http://127.0.0.1:1", "/work", task_id="tb-ev", event_logger=lg
            )
            init = response_pb2.ResponseEvent()
            init.init.conversation_id = "conv-ev"
            tc_add = response_pb2.ResponseEvent()
            tmsg = tc_add.client_actions.actions.add().add_messages_to_task.messages.add()
            tmsg.tool_call.tool_call_id = "call-1"
            tmsg.tool_call.run_shell_command.command = "ls"
            fin = response_pb2.ResponseEvent()
            fin.finished.done.CopyFrom(response_pb2.ResponseEvent.StreamFinished.Done())

            import tb_client.client as client_mod

            orig_post = client_mod.requests.post
            client_mod.requests.post = lambda *a, **kw: _sse_response([init, tc_add, fin])
            try:
                rnd = client.start("do something")
            finally:
                client_mod.requests.post = orig_post
            lg.close()

            self.assertEqual(len(rnd.tool_calls), 1)
            recs = [
                json.loads(l)
                for l in (Path(d) / "events.jsonl").read_text().strip().split("\n")
            ]
            self.assertEqual([r["type"] for r in recs], ["request", "round"])
            self.assertEqual(recs[0]["input_kinds"], ["user_query"])
            self.assertEqual(recs[1]["tool_calls"][0]["kind"], "run_shell_command")
            self.assertEqual(
                recs[1]["tool_calls"][0]["args"]["command"], "ls"
            )
            self.assertEqual(recs[1]["finish"], "done")


if __name__ == "__main__":
    unittest.main()
