# OpenWarp 架构与实现

## 两条工作流

**内置 Agent**：原生客户端 → 本地 Adapter / Agent Engine → 选定 Harness → 模型与工具循环 → 客户端展示。

**外部 Agent**：Codex / Claude Code → stdio MCP bridge → OpenWarp 本地 IPC → 授权检查 → 受管 SSH 工具。

外部 Agent 使用自己的模型与 Harness；SSH MCP 不要求它再调用 OpenWarp 内置 Harness。

## 客户端与 Adapter

原生桌面客户端负责终端展示、用户交互、SSH 配置与远端工具执行。
本地 Adapter 对接客户端协议与 Agent Runtime，处理配置、会话和流式事件。
这不是把一个网页终端嵌入 App，也不是在每台服务器部署一份 Adapter。

## 可选 Harness

Native、Pi Agent 与 DeepSeek Harness（DSH）提供不同的 Agent 运行实现。
模型服务与 Harness 是两个选择：前者生成响应，后者组织 Agent 循环与工具调用。
模型请求发往用户配置的接口；工具根据执行上下文在本地或 SSH 远端运行。

## SSH Agent

Agent 在本机运行，通过 SSH 使用远端上下文与工具。
远端无需安装完整编码 Harness，但必须具备 SSH 服务、账号权限与命令所需依赖。
执行过程可能创建临时脚本或任务状态文件，不能宣称“远端零写入”。
本机断线时不能承诺 Agent 循环持续运行；这与在远端直接运行 Harness 的方式不同。

## SSH MCP 部署示例

1. 外部 Agent 在本地测试、构建并打包项目。
2. 调用 OpenWarp MCP 上传产物到已授权服务器。
3. 调用远端命令解包、切换版本并重启预先配置的服务。
4. 读取日志与健康检查结果，确认发布状态。

修复流程则先读取远端错误，再由外部 Agent 在本地改代码、回归测试并重新部署。
网站交互演示是固定脚本：不执行 Shell、MCP 或模型请求；服务器与输出均为虚构数据。
真实部署需要单独设计备份、回滚、权限、服务定义与失败处理，不能直接照搬演示命令。

## 权限边界

MCP Access 按服务器分别授权命令、上传与下载；新服务器默认不授权。
总开关关闭后拒绝新调用，包括查询和取消，已接收命令继续执行。
上传下载开关不限制已授权 Shell 中的文件操作；服务器访问受 SSH 账号权限约束。
详见 [MCP 配置](../external-mcp/setup.md)。

## 源码入口

- [Adapter 与运行时](https://github.com/sasuke39/openwarp/tree/main/internal/agentruntime)
- [MCP bridge](https://github.com/sasuke39/openwarp/tree/main/internal/remotemcp)
- [客户端 SSH IPC 与权限](https://github.com/sasuke39/openwarp-client/tree/main/app/src/terminal/ssh/tool_ipc)
