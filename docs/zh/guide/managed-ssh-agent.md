# 托管 SSH Agent：架构约束与回归清单

## 事故结论（2026-08-25）

现象：从 SSH 页签点击 **Use agent** 后能打开 Agent，但工作目录仍显示 Mac 的
`~/stock/quant`，工具命令也可能在 Mac 执行。

根因不是 Agent 框架，而是客户端只完成了“界面进入 Agent”，没有完成“执行上下文切换”：

1. 托管 SSH 是本地 PTY 中长期运行的外层 `ssh` 进程，底层 `SessionType` 仍是 Local。
2. Agent 沿用 Local Session 的 cwd、OS、用户名和 `LocalCommandExecutor`。
3. 工具层看到长期运行块后，提前返回 `CancelledBeforeExecution`。
4. 模式切换时，SSH 启动命令还可能被提升成用户 Prompt。
5. App 恢复页签时，内存中的 SSH profile 绑定会丢失。

## 不可破坏的架构约束

“SSH Agent 可用”必须同时满足以下五项，只有 UI 出现输入框不算成功：

- **身份**：会话保存 `profile_id`，启动命令带可恢复的 profile marker。
- **上下文**：发送给 Agent 的 host、user、cwd 必须来自 SSH profile/远端，禁止回退本机 cwd。
- **执行**：Agent 工具必须走 `ManagedSshCommandExecutor`，不能走本地执行器或可见 PTY。
- **路由**：外层 SSH 块只是 transport，不能成为 LRC 子任务，也不能阻塞工具执行。
- **恢复**：Session 尚未注册时先保存绑定；注册后和工具执行前都要重新确认执行器。
- **生命周期**：外部 Harness 的 turn 跨工具往返保持活动；停止和拒绝必须调用 task cancel。
- **展示**：外层 SSH transport 不属于 conversation，Agent View 不得把它固定在所有 turn 底部。

## 安全边界

托管 SSH 当前只暴露 `run_shell_command`。没有适配远端执行的 ReadFiles、ApplyFileDiffs
等工具不得开放，否则会误读写 Mac 文件。远端文件操作暂时通过 shell 命令完成。

密码只通过现有 askpass + Keychain account 引用传递，日志和命令参数中不得出现明文密码。

## 代码责任点

- `terminal/ssh/managed_command_executor.rs`：独立 SSH 工具执行通道。
- `terminal/model/session/active_session.rs`：profile、远端 cwd、恢复和执行器绑定。
- `ai/blocklist/controller/input_context.rs`：删除本地目录污染。
- `ai/blocklist/action_model/execute/shell_command.rs`：transport 期间允许远端工具执行。
- `terminal/view.rs`：恢复 profile，并清空 SSH 启动命令，避免成为 Prompt。

## 每次发布前必须验证

1. 本地 Agent 执行 `hostname; whoami; pwd`，结果属于 Mac。
2. SSH Agent 执行同一命令，结果必须属于远端，且不能出现 `/Users/...`。
3. 配置了连接后目录时，Agent cwd 必须等于该远端目录。
4. 连接后目录为空时，使用 SSH 服务端默认目录，不能回退本机目录。
5. 重启 App、恢复 SSH 页签后重复第 2 项。
6. Agent 首条用户消息不能是 `WARPLOCAL_MANAGED_SSH=1 ... ssh ...`。
7. SSH 外层命令运行期间，`run_shell_command` 不得返回“长期命令占用”取消。
8. 断开或凭据失效时要明确报 SSH 错误，禁止静默改成本地执行。
9. 工具确认界面按停止后，Harness 必须释放 turn，下一条消息不能报 running turn。
10. 拒绝工具后重新输入自然语言，必须能开始新 turn。
11. Agent View 只显示对话和命令卡；退出后原 SSH 交互终端仍可继续使用。

最低自动化门槛：编译检查、SSH 命令生成/恢复测试、远端 cwd 测试、恢复页签进入
Agent 测试全部通过；发布验收再对一台真实 SSH 主机执行只读探针。
