# 实施与验收计划

## 阶段 0：冻结基线

- [x] 保存终端模式、Agent 模式截图和可复现 HTML。
- [ ] 为现有 App 建立可回退 tag/构建记录。
- [ ] 记录关键颜色、间距、字号和输入框尺寸。

## 阶段 1：统一输入路由

- [ ] 增加带 revision 的 `InputRouteSnapshot`。
- [ ] 调整识别优先级，覆盖中文路径命令。
- [ ] 显示自动/手动模式来源并复用现有切换控件。
- [ ] 保证 Enter 使用屏幕上显示的模式。

## 阶段 2：并行语义

- [ ] Shell 提交不再取消活动 Turn。
- [ ] Agent 活动时 AI Prompt 默认排队。
- [ ] Stop、Queue、Steer 使用不同控制接口。
- [ ] CANCELLING 期间输入只暂存，回到 IDLE 后继续。

## 阶段 3：统一时间线

- [ ] Agent View 内的用户命令生成并持久化普通 Block。
- [ ] Block、Agent Rich Content、Pending Prompt 按插入顺序混排。
- [ ] 重开 Conversation 后恢复相同顺序与可见性。
- [ ] Steer 文本和 accepted/applied/failed 状态保留在历史中。

## 阶段 4：SSH

- [ ] Warpified SSH 走结构化 Block 路径。
- [ ] 兼容 SSH 使用上 Agent、下 PTY 的明确降级布局。
- [ ] 页签切换、cwd、profile 和用户名使用提交时快照。
- [ ] 后台 Agent 工具继续使用独立 channel/job。

## 验收用例

- `ls`、`df -h /`、`ls 中文目录` 不调用 LLM并产生 Block。
- “看看当前负载”进入 Agent；手动切成终端后按 Shell 执行。
- Agent 执行 `sleep` 时运行 `pwd`，Turn 不被取消且两边都完成。
- Agent 运行时提交自然语言只进入队列；点击 Steer 后显示已接受和已应用。
- Stop 后同一 Conversation 可以开始下一 Turn。
- Warpified SSH 行为与本地一致；兼容 SSH 明确显示原始 PTY。
- App/Sidecar 重开后已完成历史和混排顺序仍存在。

## 测试要求

- Rust 输入路由、Block 可见性和 UI 状态测试。
- Go Adapter Turn/Queue/Steer 契约测试。
- 真实本机隔离 SSH：完成、失败、前台、后台、轮询、输入、取消、后续 Turn。
- 两张基线图做人工视觉对照；不得仅用 mock 宣称全链路完成。
