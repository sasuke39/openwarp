# 基线与范围

## 视觉基线

- 终端模式：`../prototypes/warp-complete-terminal.png`
- Agent 模式：`../prototypes/warp-complete-agent.png`
- 可复现页面：`../prototypes/warp-complete-input-states.html`
- 校验值与源码提交：`../prototypes/BASELINE.md`

基准视口为 1600×1000、系统缩放 100%、深色主题。动态服务器名、命令文本和时间可以变化；组件位置、尺寸、样式和状态层级必须一致。

## 包含范围

- 统一底部输入框及 Terminal/Agent 自动识别和人工切换。
- 终端 Block、Agent Turn、Tool、Queued Prompt、Steer 历史混排。
- 本地终端、Warpified SSH、Managed SSH 兼容模式。
- Agent 活动时 Shell 命令并行执行，不影响 Turn。
- Queue、Steer、Stop、失败恢复、重开 Conversation。
- 状态反馈、键盘操作、焦点、滚动和历史恢复。

## 不包含范围

- 不重写终端模拟器、Shell Integration 或 Agent 框架。
- 不新增云端能力、账户入口、MCP 或 Code Index。
- 不改变 Agent 工具权限、模型选择和记忆系统设计。
- 不通过 LLM 判断普通输入是不是命令。
- 不把前台命令按耗时自动升级为后台任务。

## 代码边界

- Warp 客户端改动位于 `warp-v0.2026.04.29.08.56.stable_00-src/...`。
- Adapter 改动只位于 `local-adapter/` 的匹配包中。
- 原型资源保留在本目录，不复制为运行时 UI 资源。
- 不修改缓存、`node_modules`、运行时配置和用户历史文件。

## 兼容要求

- 旧 Conversation 可打开、继续和停止。
- 旧 SSH Profile、钥匙串密码和历史保持可用。
- 未启用新 UI 时行为保持现状；开发期间使用 Local build gate。
- 升级前保留可回退提交和 App 构建。
