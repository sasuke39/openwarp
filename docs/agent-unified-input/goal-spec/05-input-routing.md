# 输入路由实现

## 识别优先级

1. 用户手动模式或显式前缀。
2. Shell 语法、已知命令、别名、历史和可执行首词。
3. Slash、Skill、附件等明确 Agent 语法。
4. 自然语言分类器。

“包含中文就是 Agent”必须删除。先识别首词和 Shell 结构，因此 `ls 中文目录`、`cat 配置.yaml`、`grep 错误 日志.txt` 都是终端命令。

## 并发安全

- 每次编辑增加 `revision`，分类请求携带该值。
- 回调只在 `revision` 与当前文本完全一致时更新模式。
- Enter 原子读取文本、模式、来源和执行上下文。
- 提交之后出现的分类回调不得改变已提交请求。
- 手动切换立即取消/忽略进行中的自动识别。

## Terminal 路径

```text
snapshot → current Session executor/PTY → Terminal Block/result
```

- 不经过 Adapter 和 LLM。
- 删除普通 Shell 提交对活动 Conversation 的隐式取消。
- Agent View 中创建的命令 Block 绑定当前 Conversation 可见性。
- 用户命令与 Agent 工具并发时不强制串行，但上下文和来源必须可见。

## Agent 路径

```text
snapshot → admission policy → new Turn | Queue | delayed Queue
```

- 活动 Turn 不建立第二条响应流。
- Queue 转 Steer 使用独立 API 和唯一 `steer_id`。
- Steer 只在下一 exchange 注入，不主动杀死当前工具。
- Submit/Steer 请求期间按钮禁用并显示进度，防止重复投递。

## 错误恢复

- 分类异常：保留当前可见模式并允许人工切换。
- Session 不存在：输入不丢失，提示重新选择环境。
- Agent API 失败：保留 Queue，允许重试或删除。
- 任何错误都必须包含原因和恢复操作，不能只显示“失败”。
