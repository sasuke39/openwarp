# 可行性评估

| 功能 | 可行性 | 依据与限制 |
|---|---|---|
| 单一输入框 | 高 | Warp 已有 Universal Developer Input |
| 自动识别与手动切换 | 高 | 已有 InputType、锁定和 segmented control |
| `ls` 直接执行 | 高 | 已有 Shell 路由；需修正中文优先级 |
| Agent 运行时终端命令并行 | 中高 | 执行路径存在；需停止隐式取消 Turn |
| Agent Prompt 默认排队 | 高 | 当前 Local channel 已默认 queue |
| 队列转 Steer | 高 | 前端和 Adapter API 已完成基本链路 |
| Steer 状态历史 | 高 | accepted/applied/failed 已有事件 |
| 命令与 Agent 时间线混排 | 中高 | 同一 BlockList 已支持两种实体和 Conversation 可见性 |
| 本地命令结构化 Block | 高 | 原生 Shell Integration |
| Warpified SSH Block | 高 | Remote/Warpify Session 已支持 |
| 未 Warpify SSH Block | 低 | 原始 PTY 无可靠命令边界，只能兼容分屏 |
| 后台任务与用户终端并行 | 高 | 已有显式 execution_mode、job_id 和独立通道 |

## 不建议的实现

- 不新增 Web 风格聊天消息列表再复制 Warp Block。
- 不把所有输入先发给 LLM 判断是否命令。
- 不用执行超过 N 秒自动升级为后台任务。
- 不让 Adapter 操纵用户共享 PTY。
- 不在兼容 SSH 中通过解析提示符伪造命令边界。

## 主要风险

- 分类器异步回调可能覆盖后续输入，需要 revision 防抖。
- 用户命令和 Agent 工具并发修改同一文件属于真实并发，UI 应提示但不强行串行。
- Agent View 的 Block 可见性若未持久化，重开 Conversation 后会丢失混排位置。
- SSH 页签切换时 cwd/profile 必须使用提交快照，不能读取完成时的活动页签。
- Warp 上游升级可能改动 Input、BlockList 和 AgentView，需要将补丁拆成小提交并保留视觉回归图。

## 总判断

第一版不需要重写终端或 Adapter。核心工作集中在 Warp 前端输入提交逻辑、Block 可见性和 SSH 降级展示；Adapter 只需补状态查询契约及少量防御校验。
