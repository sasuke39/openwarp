# Goal 执行入口

## Objective

在真实 WarpLocal 客户端中 1:1 复刻已冻结的统一输入原型，完整接通终端、Agent、Queue、Steer、Stop、Warpified SSH 与兼容 SSH；不得破坏当前可用能力，不得留下已知崩溃、卡死、丢输出或无法开始下一 Turn 的问题。

## 必读顺序

1. `../prototypes/BASELINE.md`
2. `01-baseline-and-scope.md`
3. `02-visual-contract.md`
4. `03-component-map.md`
5. `04-state-machine.md`
6. `05-input-routing.md`
7. `06-timeline-and-persistence.md`
8. `07-managed-ssh.md`
9. `08-adapter-contract.md`
10. `09-implementation-sequence.md`
11. `10-test-and-acceptance.md`
12. `11-delivery-runbook.md`
13. `12-implementation-record.md`

## 不可妥协项

- 原型是视觉真值；不得擅自换风格、增加阴影、渐变、发光或 SaaS 卡片。
- 统一的是输入入口和时间线，不合并终端与 Agent 的执行生命周期。
- 屏幕显示的输入模式必须等于 Enter 实际使用的模式。
- Shell 输入不得调用 LLM，也不得取消活动 Agent Turn。
- Agent 运行时普通 Prompt 默认排队，只有显式操作才 Steer。
- Stop 后同一 Conversation 必须能开始下一 Turn。
- Warpify 失败必须明确降级，不能伪造结构化 Block。
- 所有 SSH/PTY/工具变更必须通过真实本机隔离 SSH 全链路测试。

## 完成定义

- 固定环境视觉回归达到 `02-visual-contract.md` 标准。
- `10-test-and-acceptance.md` 中所有必测项通过且无跳过。
- 没有已知 P0/P1/P2 缺陷、崩溃、卡死、状态泄漏或数据丢失。
- 当前 App 的钥匙串、历史、拖拽上传、Warpify、后台任务均无回归。
- `12-implementation-record.md` 已填写真实文件、提交、测试和差异。
- 已构建、签名检查、覆盖 App，并完成启动后的人工冒烟验证。
