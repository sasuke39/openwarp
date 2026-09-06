# 外部 Agent MCP：当前实现

内置 Agent 和 MCP 复用 Go 工具转换、AST 检查，以及客户端 SSH 执行器。
MCP 不创建 Pi/DSH 会话、不输入用户 PTY；通过独立 SSH 执行通道操作远端。

## 账号和目录

- `servers_list` 返回授权服务器的 `server_id`、`name`、`username`、`default_workdir`。
- 账号、认证、跳板配置直接来自客户端已保存的 SSH 配置。
- 不返回密码、密钥内容或私钥路径，不提供切换登录账号参数。
- 未传 `workdir` 使用保存的默认目录；为空时使用远端 HOME。
- 保存的 `~`、`~/path` 在远端解析；显式 `workdir` 要求绝对路径。
- 执行前确认能够进入目录；失败不启动原命令，不回退到 `/`、不提权。
- 账号的实际权限由远端 OS 决定；工作目录不是任意 Shell 的文件系统沙箱。

## 当前工具

| 工具 | 行为 |
|---|---|
| servers_list | 返回允许使用的已保存服务器，不主动探测所有服务器 |
| workspace_shell | 原始命令、显式 foreground/background；foreground 观察 30 秒 |
| process_read | 查询原 command_id 的状态、时间和最新 64 KiB 输出快照 |
| process_write | 写入原任务 stdin；request_id 重试不重复输入 |
| process_cancel | 使用共享取消实现终止受管任务 |
| sftp_upload | 同步单文件上传，最多观察 120 秒 |
| sftp_download | 同步单文件下载，最多观察 120 秒 |

## 边界

- App 必须运行，IPC 默认关闭；UI 不是执行依赖。
- 当前 IPC 是 Unix socket；Windows Named Pipe 尚未实现。
- UI 授权页面、任务卡片订阅、独立后台 daemon 尚未实现。
- 传输当前会覆盖目标；异步 transfer_id、原子提交、默认禁止覆盖尚待实现。
- 输出当前是快照，不是游标增量；完整 PTY/TUI 交互暂不支持。
- foreground 转后台沿用现有执行器的“相同命令以 background 再提交”协议，
  不会重启进程；独立 detach 操作尚待实现。
- 去重记录在 App 内存中；重启后不承诺原请求可恢复，不能盲目重跑部署。
- `client-id` 用于执行上下文隔离，不是身份认证；同用户本地程序属于同一信任边界。
- 认证只使用已有主机信任记录，拒绝自动接受未知 SSH 主机密钥。

## 接入和测试

- [配置接入](setup.md)
- [验证方法](testing.md)

本目录记录实际实现及缺口，不把接口设计中的后续能力当作已完成。
