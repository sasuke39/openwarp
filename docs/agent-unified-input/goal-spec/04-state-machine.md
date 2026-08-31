# 状态模型

必须区分三类状态，禁止再混用 Turn、Task 和 UI 状态。

## 输入状态（前端）

```text
AUTO_TERMINAL | AUTO_AGENT | MANUAL_TERMINAL | MANUAL_AGENT
```

输入清空后恢复 Auto；手动选择只锁定当前草稿。Enter 使用当前 `InputRouteSnapshot`，旧 revision 的异步识别结果直接丢弃。

## Turn 状态（Adapter/Runtime）

```text
IDLE → RUNNING ↔ AWAITING_TOOL → COMPLETED
                  └────────────→ FAILED
RUNNING/AWAITING_TOOL → CANCELLING → CANCELLED → IDLE
```

`CANCELLING` 是取消过程，`CANCELLED` 是 Turn 的最终结果。Conversation 在 Turn 终态后回到可接收下一 Turn 的 IDLE。

## Prompt 状态（前端记录）

```text
QUEUED → SUBMITTING_STEER → ACCEPTED → APPLIED
   └──────── REMOVED          └──────→ FAILED
```

- `ACCEPTED`：Runtime 已接收，但尚未进入模型 exchange。
- `APPLIED`：已在下一 exchange 注入当前 Turn。
- 失败不删除原文，展示恢复操作。

## 提交矩阵

| 输入路由 | Turn 状态 | 行为 |
|---|---|---|
| Terminal | 任意 | 终端执行，不改变 Turn |
| Agent | IDLE/终态 | 同一 Conversation 新 Turn |
| Agent | RUNNING/AWAITING_TOOL | Queue |
| Agent | CANCELLING | 暂存 Queue，等待 IDLE |

## Stop

- IDLE/终态：幂等成功。
- RUNNING：停止模型流并等待确认。
- AWAITING_TOOL：取消 pending tool/进程并完成结果屏障。
- CANCELLING：重复 Stop 返回同一取消操作。
- CANCELLED 后立即允许同一 Conversation 创建新 Turn。
