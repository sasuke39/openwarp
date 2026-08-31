# 时间线与持久化

## 结构

统一时间线仍使用现有 BlockList：

```text
TimelineItem = TerminalBlock | AgentRichContent | PendingPrompt
```

它只决定排序和可见性，不拥有进程、Tool 或 Turn。禁止新增复制数据的聊天列表。

## 插入规则

- 用户 Shell 命令按提交时间插入普通 Block。
- Agent Prompt、回复和 Tool 行继续用 Rich Content。
- Pending Prompt 固定在当前内容之后；转 Steer 后移动为普通历史项。
- 同一时间发生的事件使用前端接收序号稳定排序，不仅依赖毫秒时间。
- 输入框固定在底部，滚动内容必须预留其高度。

## 滚动规则

- 用户位于底部时，新内容自动跟随。
- 用户向上查看历史时，不强制拉回底部，显示“跳到最新”。
- Queue、Steer 状态更新不得改变整行高度导致明显跳动。
- 输入框模式切换不得改变时间线滚动位置。

## Conversation 可见性

- Agent View 内产生的用户命令关联当前 Conversation。
- 切换 Conversation 只显示对应 Agent 内容和关联 Block。
- 回到顶层终端时遵循既有 Terminal/Agent visibility 规则。
- 兼容 SSH 的 transport Block 永远不是 Agent Tool Block。

## 持久化

- 复用现有 Block 和 Conversation 序列化，不建立额外数据库。
- 保存 Block 的 Conversation 关联和插入顺序。
- 保存已完成 Steer 的原文及最终状态。
- App/Sidecar 重启时，运行中 Turn 标记为 INTERRUPTED/CANCELLED；已完成历史恢复。
- 恢复后必须允许在同一 Conversation 开始下一 Turn。

## 清理

- 删除 Queue 只删除尚未提交的 Prompt。
- Stop 不删除已发生的输出、工具结果或 Steer 历史。
- 迟到 Tool/Steer 事件按 ID 去重并忽略，不得进入新 Turn。
