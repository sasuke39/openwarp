"""把运行记录转换成 Harbor ATIF 轨迹(plain dict,不依赖 harbor 包)。

ATIF schema 见 harbor/models/trajectories/(当前 ATIF-v1.7)。
产物由 warp_local_agent 写到 <trial>/agent/trajectory.json(harbor view 读取点)。
结构正确性由两处保证:本模块单测(字段级)+ harbor 解释器下
Trajectory.model_validate 软校验(warp_local_agent 写盘前)。

token 指标说明:adapter 的 StreamFinished 只填 Done,不带 token_usage
(Go 端 finishEvent 未填充),故 metrics / final_metrics 的 token 字段留空。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Optional

from google.protobuf.json_format import MessageToDict

SCHEMA_VERSION = "ATIF-v1.7"
ARGS_CAP = 2048  # 工具调用 args 截断上限(字符)
RESULT_CAP = 2048  # 工具结果截断上限(字符)


@dataclass
class ToolExecRecord:
    """一次工具调用及其执行结果。"""

    tool_call_id: str
    kind: str
    args: dict = field(default_factory=dict)
    result: Optional[str] = None  # 已截断的结果文本;None=未执行/未回传
    duration_sec: float = 0.0
    error: Optional[str] = None


@dataclass
class RoundRecord:
    """一次 HTTP 回合(请求→响应流→工具执行)的记录。"""

    round_no: int
    ts: str  # ISO 8601,本轮开始时间
    text: str = ""
    tool_execs: list[ToolExecRecord] = field(default_factory=list)
    finish: str = ""
    error: Optional[str] = None
    duration_sec: float = 0.0


def utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def truncate_text(s: str, cap: int) -> str:
    if len(s) <= cap:
        return s
    return s[:cap] + f"…[+{len(s) - cap} chars truncated]"


def truncate_args(args: dict, cap: int = ARGS_CAP) -> dict:
    """把工具参数压到 cap 字符内:先逐字段截断字符串,仍超则整体降级。"""
    raw = json.dumps(args, ensure_ascii=False, default=str)
    if len(raw) <= cap:
        return args
    out: dict[str, Any] = {}
    for k, v in args.items():
        if isinstance(v, str):
            out[k] = truncate_text(v, cap // 2)
        else:
            out[k] = v
    if len(json.dumps(out, ensure_ascii=False, default=str)) <= cap + 256:
        return out
    return {"_truncated_json": raw[:cap], "_note": f"args truncated to {cap} chars"}


def pb_payload_args(payload: Any) -> dict:
    """pb 工具调用消息 → dict(snake_case 字段名);失败降级为 str(payload)。"""
    try:
        return MessageToDict(payload, preserving_proto_field_name=True)
    except Exception:
        return {"_raw": str(payload)[:ARGS_CAP]}


def pb_result_text(tool_result: Any) -> str:
    """pb 工具结果消息 → JSON 文本(截断到 RESULT_CAP)。"""
    try:
        d = MessageToDict(tool_result, preserving_proto_field_name=True)
        text = json.dumps(d, ensure_ascii=False)
    except Exception:
        text = str(tool_result)
    return truncate_text(text, RESULT_CAP)


def _omit_none(d: dict) -> dict:
    return {k: v for k, v in d.items() if v is not None}


def build_atif(
    instruction: str,
    records: list[RoundRecord],
    *,
    agent_name: str,
    agent_version: str,
    session_id: Optional[str] = None,
    notes: Optional[str] = None,
    extra: Optional[dict] = None,
) -> dict:
    """构造 ATIF 轨迹 dict。records 为空时也保证 steps 至少含 user step。"""
    steps: list[dict] = []
    first_ts = records[0].ts if records else utc_now_iso()
    steps.append(
        _omit_none(
            {
                "step_id": 1,
                "source": "user",
                "timestamp": first_ts,
                "message": instruction,
            }
        )
    )
    for rec in records:
        step: dict[str, Any] = {
            "step_id": len(steps) + 1,
            "source": "agent",
            "timestamp": rec.ts,
            "message": rec.text,
        }
        if rec.tool_execs:
            step["tool_calls"] = [
                {
                    "tool_call_id": te.tool_call_id,
                    "function_name": te.kind,
                    "arguments": truncate_args(te.args),
                }
                for te in rec.tool_execs
            ]
            results = [
                _omit_none(
                    {
                        "source_call_id": te.tool_call_id,
                        "content": te.result
                        if te.error is None
                        else f"executor error: {te.error}\n{te.result or ''}",
                    }
                )
                for te in rec.tool_execs
                if te.result is not None or te.error is not None
            ]
            if results:
                step["observation"] = {"results": results}
        step["extra"] = _omit_none(
            {
                "round": rec.round_no,
                "finish": rec.finish or None,
                "error": rec.error,
                "duration_sec": round(rec.duration_sec, 3)
                if rec.duration_sec
                else None,
            }
        ) or None
        steps.append(_omit_none(step))
    return _omit_none(
        {
            "schema_version": SCHEMA_VERSION,
            "session_id": session_id,
            "agent": _omit_none(
                {"name": agent_name, "version": agent_version}
            ),
            "steps": steps,
            "notes": notes,
            "final_metrics": {"total_steps": len(steps)},
            "extra": extra,
        }
    )
