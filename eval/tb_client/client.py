"""warp-local-adapter 的无头协议客户端。

协议事实(读 cmd/server/main.go 验证过,勿凭记忆改动):
- POST {base_url}/ai/multi-agent,请求体是二进制 pb Request(proto.Unmarshal)。
- 响应是 SSE:每帧 `data: <base64.URLEncoding(proto.Marshal(ResponseEvent))>\\n\\n`。
- 一次 POST = 一轮:服务端把 LLM 输出流式发回,若是工具调用则以
  AddMessagesToTask(ToolCall) 下发,随后发 StreamFinished(Done) 结束本轮;
  客户端在容器里执行工具后,用同一 conversation_id 再发一轮 tool_call_result。
- conversation_id 首轮留空由服务端生成;task_id 建议客户端在
  task_context.tasks[0].id 里提供(服务端不再回发 CreateTask)。
- input 走 UserInputs(首轮 user_query,后续轮 tool_call_result);
  isFollowUp 由首个 input 是否为 tool_result 判定。
"""

from __future__ import annotations

import base64
import logging
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Iterable, Optional

import requests

from tb_client.pb import input_context_pb2, request_pb2, response_pb2
from tb_client.trajectory import pb_payload_args, truncate_args

log = logging.getLogger(__name__)


@dataclass
class ToolCallMsg:
    """一次工具调用(从 AddMessagesToTask 的 ToolCall 消息里拆出)。"""

    tool_call_id: str
    kind: str  # oneof 名,如 run_shell_command / read_files / apply_file_diffs
    payload: Any  # 对应的 pb 子消息


@dataclass
class Round:
    """一次 POST 收到的完整一轮事件。"""

    conversation_id: str = ""
    request_id: str = ""
    task_id: str = ""
    tool_calls: list[ToolCallMsg] = field(default_factory=list)
    finish: str = ""  # done / internal_error / max_token_limit / ...
    error: Optional[str] = None
    _text_parts: dict[str, list[str]] = field(default_factory=dict)

    @property
    def full_text(self) -> str:
        """本轮 AgentOutput 全文(首块 + AppendToMessageContent 增量,按消息 id 聚合)。"""
        return "".join("".join(parts) for parts in self._text_parts.values())


def _b64url_decode(data: str) -> bytes:
    # 服务端用 base64.URLEncoding(带 padding);容错补 '=' 后按 urlsafe 解码
    return base64.urlsafe_b64decode(data + "=" * (-len(data) % 4))


def iter_sse_payloads(lines: Iterable[str]) -> Iterable[str]:
    """把 SSE 行流折叠成逐个 data 负载(帧间以空行分隔,支持多行 data)。"""
    buf: list[str] = []
    for line in lines:
        if line == "":
            if buf:
                yield "".join(buf)
                buf = []
        elif line.startswith("data:"):
            data = line[5:]
            if data.startswith(" "):
                data = data[1:]
            buf.append(data)
    if buf:
        yield "".join(buf)


class AdapterClient:
    """与 warp-local-adapter 的单会话客户端。同步阻塞,供 asyncio.to_thread 调用。"""

    def __init__(
        self,
        base_url: str,
        workdir: str,
        *,
        home: str = "",
        os_platform: str = "Linux",
        os_distribution: str = "",
        shell_name: str = "bash",
        connect_timeout: float = 10.0,
        read_timeout: Optional[float] = 900.0,
        task_id: str = "",
        event_logger=None,
    ):
        self.base_url = base_url.rstrip("/")
        self.workdir = workdir
        self.home = home or workdir
        self.os_platform = os_platform
        self.os_distribution = os_distribution
        self.shell_name = shell_name
        self.connect_timeout = connect_timeout
        self.read_timeout = read_timeout
        self.task_id = task_id or ("tb-" + uuid.uuid4().hex[:12])
        self.conversation_id = ""  # 首轮后由 StreamInit 回填
        self._event_logger = event_logger  # tb_client.events.EventLogger 或 None
        self._round_no = 0

    # ---- 请求构造 ----

    def _build_context(self) -> input_context_pb2.InputContext:
        # 服务端 agent.WithExecutionContext 会把这些字段渲染进系统提示,
        # 让 LLM 面向容器内路径工作(而不是 adapter 所在主机)。
        ctx = input_context_pb2.InputContext()
        ctx.directory.pwd = self.workdir
        ctx.directory.home = self.home
        ctx.operating_system.platform = self.os_platform
        if self.os_distribution:
            ctx.operating_system.distribution = self.os_distribution
        ctx.shell.name = self.shell_name
        return ctx

    def build_first_request(self, instruction: str) -> request_pb2.Request:
        req = request_pb2.Request()
        # metadata.conversation_id 留空 → 服务端生成并持久化会话
        task = req.task_context.tasks.add()
        task.id = self.task_id
        task.description = "terminal-bench task"
        req.input.context.CopyFrom(self._build_context())
        user_input = req.input.user_inputs.inputs.add()
        user_input.user_query.query = instruction
        return req

    def build_tool_results_request(
        self, results: list[request_pb2.Request.Input.ToolCallResult]
    ) -> request_pb2.Request:
        if not self.conversation_id:
            raise RuntimeError("conversation_id 尚未建立,不能发送后续轮")
        req = request_pb2.Request()
        req.metadata.conversation_id = self.conversation_id
        task = req.task_context.tasks.add()
        task.id = self.task_id
        req.input.context.CopyFrom(self._build_context())
        for tr in results:
            user_input = req.input.user_inputs.inputs.add()
            user_input.tool_call_result.CopyFrom(tr)
        return req

    # ---- 收发 ----

    def start(self, instruction: str) -> Round:
        """首轮:发送任务指令。"""
        return self._post(self.build_first_request(instruction))

    def send_tool_results(
        self, results: list[request_pb2.Request.Input.ToolCallResult]
    ) -> Round:
        """后续轮:回传工具执行结果。"""
        return self._post(self.build_tool_results_request(results))

    def _post(self, req: request_pb2.Request) -> Round:
        url = f"{self.base_url}/ai/multi-agent"
        self._round_no += 1
        round_no = self._round_no
        started = time.monotonic()
        if self._event_logger is not None:
            self._event_logger.log(
                "request",
                round_no,
                input_kinds=[
                    k
                    for ui in req.input.user_inputs.inputs
                    for k in [ui.WhichOneof("input")]
                    if k
                ],
                tool_call_ids=[
                    ui.tool_call_result.tool_call_id
                    for ui in req.input.user_inputs.inputs
                    if ui.WhichOneof("input") == "tool_call_result"
                ],
            )
        resp = requests.post(
            url,
            data=req.SerializeToString(),
            headers={"Content-Type": "application/octet-stream"},
            stream=True,
            timeout=(self.connect_timeout, self.read_timeout),
        )
        resp.raise_for_status()
        rnd = self._consume(resp)
        resp.close()
        if rnd.conversation_id:
            self.conversation_id = rnd.conversation_id
        if self._event_logger is not None:
            self._event_logger.log(
                "round",
                round_no,
                text=rnd.full_text,
                tool_calls=[
                    {
                        "tool_call_id": tc.tool_call_id,
                        "kind": tc.kind,
                        "args": truncate_args(pb_payload_args(tc.payload)),
                    }
                    for tc in rnd.tool_calls
                ],
                finish=rnd.finish,
                error=rnd.error,
                duration_sec=round(time.monotonic() - started, 3),
            )
        return rnd

    def _consume(self, resp: requests.Response) -> Round:
        rnd = Round(task_id=self.task_id)
        lines = (line for line in resp.iter_lines(decode_unicode=True))
        for payload in iter_sse_payloads(lines):
            event = response_pb2.ResponseEvent()
            event.ParseFromString(_b64url_decode(payload))
            kind = event.WhichOneof("type")
            if kind == "init":
                rnd.conversation_id = event.init.conversation_id
                rnd.request_id = event.init.request_id
            elif kind == "client_actions":
                self._handle_actions(event.client_actions, rnd)
            elif kind == "finished":
                reason = event.finished.WhichOneof("reason")
                rnd.finish = reason or ""
                if reason == "internal_error":
                    rnd.error = event.finished.internal_error.message
                elif reason and reason != "done":
                    rnd.error = f"stream finished: {reason}"
                return rnd  # StreamFinished 必定是一轮的最后事件
        if not rnd.finish:
            rnd.error = "stream ended without StreamFinished"
        return rnd

    def _handle_actions(
        self, actions: response_pb2.ResponseEvent_ClientActions, rnd: Round
    ) -> None:
        for action in actions.actions:
            which = action.WhichOneof("action")
            if which == "add_messages_to_task":
                for msg in action.add_messages_to_task.messages:
                    mtype = msg.WhichOneof("message")
                    if mtype == "agent_output":
                        rnd._text_parts.setdefault(msg.id, []).append(
                            msg.agent_output.text
                        )
                    elif mtype == "tool_call":
                        tc = msg.tool_call
                        kind = tc.WhichOneof("tool")
                        if kind is None:
                            continue
                        rnd.tool_calls.append(
                            ToolCallMsg(
                                tool_call_id=tc.tool_call_id,
                                kind=kind,
                                payload=getattr(tc, kind),
                            )
                        )
            elif which == "append_to_message_content":
                msg = action.append_to_message_content.message
                if msg.WhichOneof("message") == "agent_output":
                    rnd._text_parts.setdefault(msg.id, []).append(
                        msg.agent_output.text
                    )
            elif which == "create_task":
                # 客户端已提供 task_id 时服务端不会回发;防御性记录
                rnd.task_id = action.create_task.task.id or rnd.task_id
