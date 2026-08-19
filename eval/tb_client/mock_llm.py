"""脚本化的 OpenAI 兼容 mock LLM,供无 key 的全链路 smoke 使用。

行为(确定性):
- messages 里没有 role=tool 的消息 → 流式返回一个 run_shell_command 工具调用
  (在容器里写 /work/hello.txt 并 cat)。
- 已有 role=tool 消息 → 流式返回最终文本,finish_reason=stop。
- 非流式请求(memory 提取器等)→ 返回一段固定文本。

用法:python3 -m tb_client.mock_llm [--port 18999]
"""

from __future__ import annotations

import argparse
import json
import os
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

SCRIPTED_COMMAND = "printf 'hello terminal-bench\\n' > /work/hello.txt && cat /work/hello.txt"
FINAL_TEXT = "Done: created /work/hello.txt and verified its contents with cat."
MODEL = "mock-model"


def _chunk(delta: dict, finish_reason) -> str:
    payload = {
        "id": "chatcmpl-mock",
        "object": "chat.completion.chunk",
        "created": 0,
        "model": MODEL,
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }
    return f"data: {json.dumps(payload)}\n\n"


def _tool_call_stream() -> bytes:
    args = json.dumps({"command": SCRIPTED_COMMAND, "is_read_only": False})
    tool_call = {
        "index": 0,
        "id": "call_mock_1",
        "type": "function",
        "function": {"name": "run_shell_command", "arguments": args},
    }
    out = _chunk({"role": "assistant", "tool_calls": [tool_call]}, None)
    out += _chunk({}, "tool_calls")
    out += "data: [DONE]\n\n"
    return out.encode()


def _final_stream() -> bytes:
    out = _chunk({"role": "assistant", "content": FINAL_TEXT}, None)
    out += _chunk({}, "stop")
    out += "data: [DONE]\n\n"
    return out.encode()


def _nonstream_completion() -> bytes:
    return json.dumps(
        {
            "id": "chatcmpl-mock",
            "object": "chat.completion",
            "created": 0,
            "model": MODEL,
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "mock notes"},
                    "finish_reason": "stop",
                }
            ],
        }
    ).encode()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length) or b"{}")
        messages = body.get("messages", [])
        stream = bool(body.get("stream"))
        has_tool_result = any(m.get("role") == "tool" for m in messages)
        self.log_message(
            "mock-llm: stream=%s msgs=%d has_tool_result=%s",
            stream,
            len(messages),
            has_tool_result,
        )
        # 超时路径自测:MOCK_LLM_DELAY_SEC>0 时,含 tool 结果的请求(第 2 轮起)
        # 先睡这么久再响应,让调用方有机会被 cancel
        delay = float(os.environ.get("MOCK_LLM_DELAY_SEC", "0") or 0)
        if delay > 0 and has_tool_result:
            time.sleep(delay)
        if not stream:
            data = _nonstream_completion()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
        else:
            data = _final_stream() if has_tool_result else _tool_call_stream()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):  # 打到 stderr,便于 smoke 观察
        import sys

        sys.stderr.write(fmt % args + "\n")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18999)
    args = ap.parse_args()
    HTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
