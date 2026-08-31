# Adapter 契约

## 职责

Adapter 不负责输入分类和用户直接 Shell 命令。它只负责 Agent Conversation、Turn、Queue/Steer 控制、工具结果屏障、取消和 Runtime 适配。

## 前端需要的状态

```text
ConversationState
  conversation_id
  active_turn_id?
  phase: idle | running | awaiting_tool | cancelling
  can_start_turn
  can_queue
  can_steer
  can_stop
```

状态必须来自一个规范入口；前端不得再通过 `task`、加载图标或按钮状态猜测 Turn。

## 控制接口

- `start/continue`：仅在没有活动 Turn 时创建新 Turn。
- `queue`：记录下一 Turn Prompt，不打开响应流。
- `steer`：绑定活动 Turn 和唯一 `steer_id`。
- `steer status`：返回 accepted/applied/failed/cancelled。
- `cancel`：幂等，等待 Runtime 和 pending tools 到安全终态。

## 串行与屏障

- 每个 Conversation 的 Agent 请求按到达顺序串行。
- 一批 Tool Call 的每个 ID 必须得到 success/error/rejected/cancelled/timeout。
- 未完成 Tool 有客户端超时和 Adapter 最终保险超时。
- Stop 把未完成 Tool 变成 cancelled，并丢弃迟到结果。
- 一批结果完成后才恢复框架 exchange；background 启动成功立即返回 job_id。

## Runtime 适配

- Pi/DSH 原生 Steer 映射到各自接口；没有原生能力时在下一 exchange 注入。
- Runtime 结束但没有 assistant response 时必须转成明确失败并释放 Turn。
- Sidecar/进程重启不复活半截 Turn；标记 interrupted，恢复完成历史。
- 任何 Runtime 错误后都必须允许同一 Conversation 的下一 Turn。

## 可观察性

日志包含 conversation_id、turn_id、tool_call_id、steer_id、job_id 和状态变更；不得记录 API Key、密码或完整 AskPass 内容。
