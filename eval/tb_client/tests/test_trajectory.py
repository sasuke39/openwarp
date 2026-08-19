"""ATIF 轨迹构建的单测(结构校验 + 截断 + 超时残缺轮)。

若本环境能 import harbor(如在 harbor 解释器下跑),额外用官方模型做严格校验。
"""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # eval/ 上 sys.path

from tb_client.trajectory import (
    RoundRecord,
    ToolExecRecord,
    build_atif,
    truncate_args,
    truncate_text,
    utc_now_iso,
)


def _harbor_trajectory_model():
    try:
        from harbor.models.trajectories.trajectory import Trajectory

        return Trajectory
    except Exception:
        return None


class TruncateTest(unittest.TestCase):
    def test_truncate_text(self):
        self.assertEqual(truncate_text("abc", 10), "abc")
        out = truncate_text("x" * 3000, 2048)
        self.assertTrue(out.startswith("x" * 2048))
        self.assertIn("truncated", out)

    def test_truncate_args_small(self):
        args = {"command": "ls", "is_read_only": True}
        self.assertEqual(truncate_args(args), args)

    def test_truncate_args_long_string_field(self):
        args = {"command": "y" * 10000}
        out = truncate_args(args)
        self.assertLess(len(json.dumps(out, ensure_ascii=False)), 2048 + 512)


class BuildAtifTest(unittest.TestCase):
    def _records(self):
        r1 = RoundRecord(
            round_no=1,
            ts=utc_now_iso(),
            text="我先看一下文件",
            finish="done",
            duration_sec=1.5,
        )
        r1.tool_execs.append(
            ToolExecRecord(
                tool_call_id="call-1",
                kind="run_shell_command",
                args={"command": "ls -la"},
                result='{"command_finished": {"output": "a.txt", "exit_code": 0}}',
                duration_sec=0.5,
            )
        )
        r2 = RoundRecord(
            round_no=2, ts=utc_now_iso(), text="做完了", finish="done", duration_sec=0.8
        )
        return [r1, r2]

    def test_structure(self):
        traj = build_atif(
            "创建 hello.txt",
            self._records(),
            agent_name="warp-local-adapter",
            agent_version="0.2.0",
            session_id="conv-1",
            notes="n",
        )
        self.assertEqual(traj["schema_version"], "ATIF-v1.7")
        self.assertEqual(traj["session_id"], "conv-1")
        self.assertEqual(traj["agent"]["name"], "warp-local-adapter")
        steps = traj["steps"]
        self.assertEqual([s["step_id"] for s in steps], [1, 2, 3])
        self.assertEqual(steps[0]["source"], "user")
        self.assertEqual(steps[0]["message"], "创建 hello.txt")
        agent_step = steps[1]
        self.assertEqual(agent_step["source"], "agent")
        self.assertEqual(agent_step["message"], "我先看一下文件")
        tc = agent_step["tool_calls"][0]
        self.assertEqual(tc["tool_call_id"], "call-1")
        self.assertEqual(tc["function_name"], "run_shell_command")
        self.assertEqual(tc["arguments"]["command"], "ls -la")
        obs = agent_step["observation"]["results"][0]
        self.assertEqual(obs["source_call_id"], "call-1")
        self.assertIn("exit_code", obs["content"])
        self.assertEqual(agent_step["extra"]["duration_sec"], 1.5)
        self.assertEqual(steps[2]["message"], "做完了")
        self.assertNotIn("tool_calls", steps[2])
        self.assertEqual(traj["final_metrics"]["total_steps"], 3)

    def test_partial_round_timeout(self):
        """超时场景:工具调用没有结果,observation 只能引用已知的 call id。"""
        r1 = RoundRecord(round_no=1, ts=utc_now_iso(), text="", finish="")
        r1.tool_execs.append(
            ToolExecRecord(
                tool_call_id="call-x",
                kind="run_shell_command",
                args={"command": "sleep 999"},
                result=None,  # 被执行前就被 cancel
            )
        )
        traj = build_atif(
            "instr",
            [r1],
            agent_name="a",
            agent_version="1",
            session_id="s",
        )
        step = traj["steps"][1]
        self.assertIn("tool_calls", step)
        self.assertNotIn("observation", step)  # 无结果则不写 observation
        model = _harbor_trajectory_model()
        if model is not None:
            model.model_validate(traj)  # 残缺轮也必须通过官方校验

    def test_empty_records(self):
        traj = build_atif("instr", [], agent_name="a", agent_version="1")
        self.assertEqual(len(traj["steps"]), 1)  # 至少 user step
        model = _harbor_trajectory_model()
        if model is not None:
            model.model_validate(traj)

    def test_harbor_model_validate_full(self):
        model = _harbor_trajectory_model()
        if model is None:
            self.skipTest("harbor 不在当前解释器里")
        traj = build_atif(
            "创建 hello.txt",
            self._records(),
            agent_name="warp-local-adapter",
            agent_version="0.2.0",
            session_id="conv-1",
        )
        parsed = model.model_validate(traj)
        self.assertEqual(len(parsed.steps), 3)


if __name__ == "__main__":
    unittest.main()
