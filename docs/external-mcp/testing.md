# MCP 与真实 SSH 验证

运行（需要 Go、Rust、Node、OpenSSH，Pi/DSH sidecar 已构建）：

```bash
bash scripts/test-mcp-local-ssh.sh
```

脚本启动仅监听 loopback 的隔离 sshd，使用临时 host key 和登录 key。
不修改系统 sshd、用户 authorized_keys、生产服务器或已保存服务器配置。
退出时停止 sshd 并移除临时凭据。

Rust 测试启动生产 IPC 服务，使用临时保存配置代替用户配置文件。
Go 测试启动真实 `warp-local-adapter mcp` stdio 子进程，使用 MCP SDK 客户端调用。
执行端是客户端原有 ManagedSshCommandExecutor，不是 mock SSH 或本地 Bash 替代品。

## 覆盖

- MCP 初始化、七个工具的发现、Schema 参数校验与 AST 拒绝。
- 服务器列表包含保存账号和目录、不包含认证字段。
- 保存默认目录、显式覆盖、空目录回退远端 HOME。
- 进入错误目录后原命令不执行，非零退出码正确返回。
- 同 request_id 重试不重复执行，参数冲突拒绝。
- 未授权服务器、越权文件路径拒绝。
- background 启动、输入、输出查询、取消、后续命令。
- MCP 断开重连后读取已有任务。
- foreground 超过 30 秒返回 running，查询到退出后能继续执行。
- SFTP 含空格路径上传下载，核对实际文件内容一致。
- Native、Pi、DSH 真实模型协议回合、工具结果、最终答复及后续 Turn。

模型端使用本地确定性 HTTP endpoint；三个 harness 本身不做 mock。
没有 fixture 时相关集成测试会 skip；普通 `go test ./...` 通过不能代替此脚本。
不把未执行的平台测试或尚未实现的完整接口设计计入覆盖率。

## 已打包 App 的只读列表验收

启动启用了临时 IPC 的真实 App 后，设置以下三个环境变量：

- `WARPLOCAL_PACKAGED_MCP_BIN`：包内 `Contents/Helpers/warp-local-adapter`。
- `WARPLOCAL_PACKAGED_MCP_SOCKET`：该 App 的 IPC socket。
- `WARPLOCAL_PACKAGED_SSH_PROFILES`：客户端的已保存 SSH 配置文件路径。

运行 `go test ./internal/remotemcp -run '^TestPackagedServerList$' -count=1 -v`。
此用例启动包内 MCP stdio，调用 tools/list 和 servers_list，核对全部保存配置的
名称、账号、默认目录及执行支持标记；不调用 shell 或 SFTP。
交互式堡垒机应列出并标记不可执行，不能直接从列表中消失。
日志只输出 MCP 返回的公开元数据，不输出认证字段；验收后关闭临时授权。
