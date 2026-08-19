# 03-LLM 交互流程

## 完整数据流

```
用户输入"帮我修这个 bug"
    ↓
[第 1 轮] adapter 调 LLM,工具列表里有 update_task_list
    ↓
LLM 返回 text + tool_call(update_task_list, tasks=[{id:"step-1",content:"读代码",status:"in_progress",...}, ...])
    ↓
adapter 校验(单 in_progress、id 唯一...) -> 校验通过,存到 conv.todoList
    ↓
adapter 把"任务列表已更新,当前进行中:step-1 读代码"作为工具结果返回给 LLM
    ↓
[第 2 轮] LLM 看到"step-1 读代码 进行中",调 read_files 读代码
    ↓
read_files 结果返回,LLM 继续分析...
    ↓
[第 N 轮] LLM 认为 step-1 做完了,调 update_task_list 把 step-1 标 completed、step-2 标 in_progress
    ↓
adapter 校验 -> 更新 conv.todoList -> 返回"step-1 已完成,当前进行中:step-2 ..."
    ↓
... 循环 ...
    ↓
[最后] LLM 调 update_task_list 把所有标 completed,然后输出最终答案
```

## 关键：每轮注入当前任务状态

每次 LLM 返回工具调用结果后，adapter 在下一轮请求的 system prompt 后面追加一段"当前任务状态":

```
## Current Task Progress
- [x] step-1: 读代码 (completed)
- [>] step-2: 写修复 (in_progress)   ← 当前
- [ ] step-3: 跑测试 (pending)
- [ ] step-4: 提交 (pending)
```

这段不是写进 conv.history（避免污染），而是**每轮动态拼到 system prompt 尾部**（和执行环境后缀同一个位置）。

效果：模型每一轮都能看到"我现在在 step-2，做完了该标完成"。这是治"走偏不自知"的核心机制。

## 状态流转图

```
pending ──→ in_progress ──→ completed
   │              │
   │              └──→ cancelled(放弃这个任务)
   │
   └──→ cancelled(还没开始就放弃)

completed 不可回退;cancelled 不可回退;pending 可改成 in_progress 或 cancelled
```

## 异常处理

- **LLM 忘记调 update_task_list**：不强制。模型可以整个任务都不调（简单任务不需要 todo)。提示词里写"复杂任务建议先列计划"，但不强制。
- **LLM 调了但没做 todo 里的事**：不管。todo 只是模型自己的规划，adapter 不验证"你真的做了 step-1 吗"。
- **LLM 把所有标 completed 但实际没做完**：不管。这是模型自己的判断，adapter 不审计。

## 和现有 agent loop 的集成点

`runAgentLoop` 里 LLM 返回工具调用后：

1. 遍历 `result.ToolCalls`，如果有一个是 `update_task_list`:
   - 走校验逻辑
   - 校验通过 -> 更新 `conv.todoList` -> 工具结果返回成功
   - 校验失败 -> 工具结果返回错误信息，LLM 下一轮会看到并修正
2. 所有工具结果返回后，下一轮请求前，把 `conv.todoList` 格式化成"Current Task Progress"追加到 system prompt 尾部
