# 启用本地 MCP 工具

先构建包含本功能的客户端与 Adapter；不需要修改现有模型配置。
当前没有开关 UI，用显式本地策略文件启用。

## 客户端

1. 创建仅当前用户可访问的 IPC 目录（权限 0700）。
2. 创建目录下的 `policy.json`（权限 0600）：

```json
{
  "enabled": true,
  "server_ids": ["已保存 SSH 配置的 id"],
  "transfer_roots": ["/absolute/build-output"]
}
```

3. 启动客户端时设置 `WARPLOCAL_TOOL_IPC_DIR` 指向这个目录。
4. 成功启动后目录下出现 `tools.sock`。

没有指定环境变量时，目录使用系统临时目录下的 `warplocal-tools-<uid>`。
macOS 的 Unix socket 路径长度有限；自定义目录建议使用简短绝对路径。
不需要在此再次填写账号、密码、密钥和默认目录。

`server_ids` 是外部使用授权，不改变 SSH 账号在服务器上的权限。
撤销服务器授权或设置 enabled=false 对后续调用生效；新增授权需重启 App。
修改已使用的服务器配置后，当前版本要求重启 App，避免旧任务切换到新目标。
停用服务不自动终止已启动的远端任务。

## MCP stdio 配置

在支持 MCP stdio 的客户端中设置 executable 和 arguments：

```json
{
  "command": "/absolute/path/warp-local-adapter",
  "args": [
    "mcp", "--socket", "/absolute/private-directory/tools.sock",
    "--client-id", "codex"
  ]
}
```

Claude Code 使用不同的稳定 `--client-id`，例如 `claude-code`。
同一个调用方重连时保留该值，才能访问原执行上下文中的任务。
字段外围结构按调用客户端的 MCP 配置格式填写。

MCP 桥接的 stdout 只用于协议，诊断写 stderr；无需模型 API Key。
客户端不可用时明确报错，不启动另一个执行服务。
已有 socket 不会自动删除，防止抢占另一 App；异常退出后确认原 App 已停止，
再移除该具体 stale socket。不要删除整个状态目录。
