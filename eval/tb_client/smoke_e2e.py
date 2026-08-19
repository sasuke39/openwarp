"""端到端 smoke:真实 adapter + 一个 alpine 容器,跑一道自造小题。

不起 Harbor,直接用 DockerExecEnv 鸭子类型验证 客户端↔adapter↔LLM↔容器 全链路。
用法(在 eval/ 目录):
    python3 -m tb_client.smoke_e2e [--adapter http://127.0.0.1:18889] [--rounds 12]
前置:adapter 已在 --adapter 端口上运行(config 已填真实 api_key)。
"""

from __future__ import annotations

import argparse
import asyncio
import subprocess
import sys
import uuid

from tb_client.client import AdapterClient
from tb_client.executor import ContainerExecutor
from tb_client.local_env import DockerExecEnv

INSTRUCTION = (
    "You are in a Linux container. Do exactly this: "
    "1) create the file /work/hello.txt containing the single line 'hello terminal-bench' "
    "using run_shell_command; "
    "2) verify it with cat; "
    "3) reply with one short sentence confirming the file contents. "
    "Do not ask questions."
)
EXPECTED = "hello terminal-bench"


def _docker(*args: str) -> str:
    return subprocess.run(
        ["docker", *args], capture_output=True, text=True, check=True
    ).stdout.strip()


async def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--adapter", default="http://127.0.0.1:18889")
    ap.add_argument("--rounds", type=int, default=12)
    args = ap.parse_args()

    name = f"wl-smoke-{uuid.uuid4().hex[:8]}"
    print(f"[smoke] 启动 alpine 容器 {name}")
    try:
        _docker("run", "-d", "--name", name, "alpine:3.20", "sleep", "600")
    except subprocess.CalledProcessError as exc:
        print(f"[smoke] docker run 失败: {exc.stderr}", file=sys.stderr)
        return 2
    try:
        env = DockerExecEnv(name, workdir="/work")
        await env.exec("mkdir -p /work", cwd="/")  # -w /work 在目录不存在时会直接失败
        client = AdapterClient(args.adapter, "/work", os_platform="Linux")
        executor = ContainerExecutor(env, "/work", sync_wait=30)

        rnd = await asyncio.to_thread(client.start, INSTRUCTION)
        n_tools = 0
        for round_no in range(1, args.rounds + 1):
            if rnd.error:
                print(f"[smoke] 第 {round_no} 轮出错: {rnd.error}", file=sys.stderr)
                return 1
            if not rnd.tool_calls:
                break
            results = []
            for tc in rnd.tool_calls:
                n_tools += 1
                print(f"[smoke] 第 {round_no} 轮工具: {tc.kind}")
                results.append(await executor.execute(tc))
            rnd = await asyncio.to_thread(client.send_tool_results, results)
        else:
            print("[smoke] 超过轮数上限", file=sys.stderr)
            return 1

        final = rnd.full_text
        print(f"[smoke] 完成: rounds 内工具调用 {n_tools} 次")
        print(f"[smoke] 最终回复: {final[:300]}")

        check = await env.exec("cat /work/hello.txt")
        content = (check.stdout or "").strip()
        ok = content == EXPECTED and bool(final)
        print(
            f"[smoke] 容器内 /work/hello.txt = {content!r} → {'PASS' if ok else 'FAIL'}"
        )
        return 0 if ok else 1
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        print(f"[smoke] 已清理容器 {name}")


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
