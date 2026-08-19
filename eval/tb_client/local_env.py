"""鸭子类型的最小 environment 实现,供 smoke / 单测使用(不依赖 Harbor)。

两个类都只实现 ContainerExecutor 用到的 `async exec(command, cwd=...,
timeout_sec=...)` 接口,返回带 stdout/stderr/return_code 的对象。
"""

from __future__ import annotations

import asyncio
import os
from types import SimpleNamespace


class LocalSandbox:
    """在本机一个目录里用 /bin/sh 执行命令(不隔离,仅用于 /tmp 下的 smoke)。"""

    def __init__(self, root: str):
        self.root = root
        os.makedirs(root, exist_ok=True)

    async def exec(self, command, cwd=None, env=None, timeout_sec=None, user=None):
        proc = await asyncio.create_subprocess_shell(
            command,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=cwd or self.root,
        )
        try:
            out, err = await asyncio.wait_for(proc.communicate(), timeout_sec)
        except asyncio.TimeoutError:
            proc.kill()
            await proc.wait()
            return SimpleNamespace(
                stdout="", stderr=f"timeout after {timeout_sec}s", return_code=124
            )
        return SimpleNamespace(
            stdout=out.decode(errors="replace"),
            stderr=err.decode(errors="replace"),
            return_code=proc.returncode,
        )


class DockerExecEnv:
    """对一个已在运行的 docker 容器执行 `docker exec`(smoke 用 alpine 容器)。"""

    def __init__(self, container: str, workdir: str = "/work"):
        self.container = container
        self.workdir = workdir

    async def exec(self, command, cwd=None, env=None, timeout_sec=None, user=None):
        args = ["docker", "exec", "-w", cwd or self.workdir, self.container, "sh", "-c", command]
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            out, err = await asyncio.wait_for(proc.communicate(), timeout_sec)
        except asyncio.TimeoutError:
            proc.kill()
            await proc.wait()
            return SimpleNamespace(
                stdout="", stderr=f"timeout after {timeout_sec}s", return_code=124
            )
        return SimpleNamespace(
            stdout=out.decode(errors="replace"),
            stderr=err.decode(errors="replace"),
            return_code=proc.returncode,
        )
