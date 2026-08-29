# 技术架构

## 边界

```text
统一输入框
  └─ InputRouter（前端）
       ├─ terminal → Terminal/SSH Executor → Warp Block 或原始 PTY
       └─ agent    → Conversation Policy → Adapter → Agent Runtime
```

两条路径共享 `ExecutionContext`，不共享执行状态：

```text
ExecutionContext = host/profile + user + cwd + shell + conversation_id
```

提交时保存上下文快照，避免用户切换 SSH 页签或目录后把结果记到错误位置。

## 前端改动

- 以现有 `BlocklistAIInputModel` 为核心增加 `InputRouteSnapshot`。
- 复用现有 Terminal/Agent segmented control，显示自动/手动来源。
- Enter 只消费当前可见快照，不在提交瞬间暗中重新分类。
- Shell 路径不得触发 `CancellationReason::UserCommandExecuted`。
- AI 路径继续复用 queue、submit 和 Steer API。
- 新命令 Block 在 Agent View 中绑定当前 Conversation 可见性。
- Rich Content 与命令 Block 继续使用现有 BlockList 排序，不新增第二套消息列表。

## Adapter 改动

- 不承担文本分类，也不执行用户直接输入的普通终端命令。
- 暴露稳定的 Conversation/Turn 状态快照供前端决定 submit、queue 或暂存。
- 继续保证每个 Conversation 串行处理 Agent 输入。
- 继续用独立控制接口处理 Stop 和 Steer。
- 保留显式 foreground/background Tool Schema 与后台 job 控制。

## Managed SSH

- Warpified Remote：走正常 Session command executor，生成 Block。
- 兼容模式：写入当前 transport PTY，只提供原始终端语义。
- Agent 工具仍走独立远程执行通道，不能复用用户 PTY。
- 后台工具任务继续创建独立 SSH channel/job，不阻塞用户 PTY。

## 展示与执行解耦

时间线项目只引用既有实体：

```text
TimelineItem = TerminalBlock | AgentRichContent | PendingPrompt
```

时间线决定顺序和可见性，不接管进程、工具调用或 Turn。这样即使 UI 重排，也不会再次引入“终端卡住导致 Agent 生命周期卡死”的问题。
