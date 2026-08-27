# Agent Runtime 生命周期 TODO

## 核心模型

- [x] 明确 `Conversation -> Turn -> Exchange` 归属；同会话最多一个活跃 Turn。
- [ ] 统一状态：`IDLE/RUNNING/AWAITING_TOOL/CANCELLING`。
- [ ] 统一终态：`COMPLETED/FAILED/CANCELLED`，终态后原会话可开启新 Turn。
- [ ] 所有事件携带并校验 conversation、turn、exchange 标识，隔离迟到事件。
- [x] 每个 Conversation 使用串行控制队列；不同 Conversation 仍可并行。

## Stop 与工具结果

- [ ] Stop 覆盖生成、等待审批、等待工具、重试等阶段。
- [x] `Cancel` 必须等待 Sidecar 确认，不能只确认控制帧已写入。
- [x] Pi 执行 `abort -> waitForIdle -> turn.cancelled`。
- [x] DSH 关闭当前 Harness Run 后返回 `turn.cancelled`。
- [x] Stop 幂等；完成后同一 Conversation 的下一 Turn 可正常运行。
- [x] Reject 返回结构化 `tool.result(status=rejected)` 并清理 pending tool。
- [x] Reject 不结束 Conversation，也不污染下一 Turn。
- [x] 多工具调用按批次登记并按原始顺序一次性恢复框架。
- [x] 客户端遗漏的工具结果补为 `error`，重复和迟到结果不再恢复框架。
- [x] Managed SSH 的 `workspace.read_file` 回退为远端只读 `sed` 命令。

## Steer 第一阶段

- [x] 增加 `user.steer` 输入，不创建新 Turn。
- [x] 工具运行期间暂存 Steer，在下一 exchange/LLM 安全边界注入。
- [x] 本阶段不取消 PTY、SSH 命令，不实现 `tool.cancel`。

## 命令执行通道

- [x] `auto/foreground` 等待命令真实退出，不按运行时长猜测后台任务。
- [x] `background` 必须由框架显式选择，并创建独立 SSH 执行通道。
- [x] 后台命令返回 `command_id`，支持读取输出、写入 stdin 和终止进程组。
- [x] 后台命令不占用用户可见 PTY，后续普通命令可继续执行。
- [x] 使用 Bash AST 在执行前拒绝未闭合 heredoc、引号和语法错误。
- [ ] App 将后台任务状态展示为可展开 Block，并提供停止入口。

## 框架边界与恢复

- [ ] Native、Pi、DSH 映射相同的上层生命周期和终态。
- [ ] App 改用 DSH 支持的 Node 22.19 或 24+，替换当前 Node 23.6。
- [x] Compaction 继续由当前框架负责，不进入客户端核心状态机。
- [x] Pi 压缩预算随上下文窗口调整；长度终止必须释放 Turn。
- [x] 保持现有 Pi/DSH 临时 Session 目录，不迁移存储位置。
- [ ] Sidecar 重启恢复已完成历史；未完成 Turn 标记中断/取消后允许继续。

## 验收

- [ ] 生成、等待工具、工具拒绝阶段均可 Stop，重复 Stop 无副作用。
- [x] Stop/Reject 后同一 Conversation 可立即开启下一 Turn。
- [x] Tool Result、Stop、新 Turn 并发到达时按会话顺序处理。
- [x] 旧 Turn 的重复或迟到工具结果不会进入新 Turn。
- [ ] 旧 Turn 的迟到模型事件不会进入新 Turn。
- [ ] Steer 在下一 exchange 生效且不终止当前客户端命令。
- [ ] Pi/DSH Sidecar 重启后能用相同 Session 继续已完成历史。
- [ ] 不再出现 `already processing` 和错误的空响应判定。
