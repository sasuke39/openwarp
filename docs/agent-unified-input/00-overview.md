# Agent 统一输入设计基线

## Goal 执行文档

- 从 [`goal-spec/00-goal.md`](goal-spec/00-goal.md) 开始；该入口定义必读顺序、硬约束和完成标准。
- 执行过程中必须填写 [`goal-spec/12-implementation-record.md`](goal-spec/12-implementation-record.md)，没有测试证据不得宣称完成。

## 结论

- 方案可实现，且 Warp 已具备大部分基础组件。
- 统一的是输入入口和时间线，不合并终端与 Agent 的执行生命周期。
- 前端负责识别、人工切换、展示和命令投递；Adapter 负责 Turn、队列、Steer 与工具生命周期。
- Warpify 成功时可实现完整结构化 Block；兼容 SSH 只能保留原始 PTY，不伪造 Block。

## 已保存原型

- `prototypes/warp-complete-terminal.png`：终端命令状态。
- `prototypes/warp-complete-agent.png`：Agent 指令状态。
- `prototypes/warp-complete-input-states.html`：可复现的完整页面。

## 已确认的现有能力

- `BlocklistAIInputModel` 已支持 Shell、AI、自动识别和人工锁定。
- `UniversalDeveloperInputButtonBar` 已有 Terminal/Agent 切换控件。
- Warp BlockList 已能按 Conversation 混排终端 Block 与 Rich Content。
- Queued Prompt、移除队列和 Steer next exchange 已存在。
- Adapter 已有 Active Turn、Steer 接受/应用状态和显式后台任务。
- Managed SSH 已有 Warpify、兼容 PTY 和独立远程命令执行器。

## 当前主要缺口

- 普通终端命令仍可能取消正在运行的 Agent Conversation。
- Managed SSH 兼容模式把命令直接写入 transport PTY，不能生成可靠 Block。
- 现有中文规则过强，`ls 中文目录` 也可能被误判为 Agent。
- 自动识别是异步的，提交时必须绑定用户实际看到的模式。
- Agent 全屏态、终端态和 SSH 兼容态的展示规则尚未统一。

## 设计原则

- 模式在提交前可见，Enter 严格执行屏幕上显示的模式。
- 自动识别永远可被人工切换覆盖。
- Agent 正在运行时，普通 Prompt 默认排队；Steer 必须显式点击。
- 用户终端命令不应隐式 Stop 或取消 Agent Turn。
- 只合并展示时间线，不让两个执行器共用一个生命周期。
