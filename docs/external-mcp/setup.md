# 启用本地 MCP 工具

先构建包含本功能的客户端与 Adapter；不需要修改现有模型配置。
新版客户端通过 **Settings → MCP Access** 管理授权；旧客户端需升级后使用此页面。

## 推荐接入流程

1. 在 OpenWarp 保存 SSH 连接；不需要再次提供服务器密码或私钥。
2. 打开 **Settings → MCP Access**，启用 MCP。
3. 按服务器分别打开命令执行、上传、下载权限；新连接默认不授权。
4. 选择 Codex 或 Claude Code，点击“复制接入配置”，合并到对应工具的 MCP 配置。
5. 在外部工具中重连 MCP；使用期间保持 OpenWarp 运行。

开关和服务器权限对后续调用生效，无需重启 App。关闭后拒绝所有新调用，
包括查询、输入和取消；已接收的命令继续执行，不会被自动终止。
页面不自动修改外部工具配置，也不将 client-id 视为身份认证。

## 手工策略与隔离测试

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

没有指定环境变量时，Socket 使用系统临时目录下的 `warplocal-tools-<uid>`。
授权策略使用 `~/.warp-local/tool-ipc/policy.json`，首次升级迁移已有旧策略。
显式指定目录时仍从该目录读取策略，适用于隔离测试。
实现和兼容说明见 [策略持久化](persistent-policy.md)。
macOS 的 Unix socket 路径长度有限；自定义目录建议使用简短绝对路径。
不需要在此再次填写账号、密码、密钥和默认目录。

`server_ids` 是外部使用授权，不改变 SSH 账号在服务器上的权限。
上传允许当前用户可读取的任意本地普通文件，不检查 `transfer_roots`。
`transfer_roots` 仅限制下载写入的本地目录；空列表禁止下载。
新版支持 `server_permissions`：按服务器 id 指定 `command`、`upload`、`download` 布尔值。
显式权限优先于旧 `server_ids`；没有显式项的旧授权兼容为三项全开。
目录限制仅指本地下载落点，服务器路径由 SSH 账号权限决定。
上传/下载开关只限制对应传输工具，不限制已授权 Shell 命令的文件操作。
手动扩大下载根目录后需重启；设置页不提供目录编辑。
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
