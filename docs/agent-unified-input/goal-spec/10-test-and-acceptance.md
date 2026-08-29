# 测试与验收

## 路由测试

- `ls`、`df -h /`、`git status`、`ls 中文目录`、`cat 配置.yaml` → Terminal。
- “看看当前负载”、英文自然语言、单词追问 → Agent。
- 自动判断后手动切换，Enter 严格按手动结果。
- 快速输入/删除/粘贴时旧分类回调不能覆盖新文本。

## 生命周期测试

- Agent RUNNING 时 Shell 命令成功，Turn 继续。
- RUNNING/AWAITING_TOOL 时 Agent Prompt 进入 Queue。
- Queue 删除不提交；转 Steer 后依次显示 accepted/applied。
- Steer 失败保留原文并可重试。
- Stop 可发生在生成、审批、工具执行和取消中；之后新 Turn 正常。
- Tool reject、timeout 和迟到结果不影响下一 Turn。

## 时间线与恢复

- Terminal/Agent/Tool/Queue 按发生顺序混排。
- 用户上滚时新输出不抢滚动位置。
- 切换 Conversation 和重开 App 后顺序、内容、状态一致。
- stdout/stderr、退出码、长输出和无输出命令正确展示。

## 真实本机 SSH（强制）

- 启动隔离 SSH 服务，经 Managed SSH 同路径连接。
- 覆盖成功、失败、foreground、background、poll、input、cancel。
- 覆盖 Warpified 和强制兼容模式。
- 覆盖取消后下一 Turn、Keychain 路径和文件上传。
- 测试状态放在临时目录，结束后清理；不得用外部服务器替代。

## 视觉与可用性

- 两张 1600×1000 golden 均达到像素标准。
- 模式不只靠颜色；键盘焦点可见；按钮提交中不可重复点击。
- 80–150ms 内出现按压反馈；超过 300ms 显示等待状态。
- 终端缩放、窗口变窄、长中文和长路径不溢出。

## 通过门槛

客户端相关测试、Adapter Go 测试、Pi/DSH 测试、真实 SSH、视觉回归和人工冒烟全部通过且无跳过。任何失败、flaky、未验证分支或已知 P0/P1/P2 缺陷都表示 Goal 未完成。
