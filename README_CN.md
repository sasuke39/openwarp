<p align="right"><a href="./README.md">English</a> · 简体中文</p>
<p align="center"><img src="./docs/public/openwarp-icon.png" width="72" alt="OpenWarp"></p>
<h1 align="center">你的终端，你的 AI。</h1>
<p align="center">OpenWarp — 本地优先的智能终端。模型、Agent 框架与 SSH 工具，由你选择。</p>
<p align="center"><a href="https://sasuke39.github.io/openwarp/zh/">产品网站</a> · <a href="https://github.com/sasuke39/openwarp/releases/latest">下载 Mac 版</a> · <a href="https://sasuke39.github.io/openwarp/zh/guide/getting-started">使用文档</a></p>

<p align="center"><img src="./docs/agent-unified-input/prototypes/warp-complete-agent.png" alt="OpenWarp 示意界面：Agent 操作与终端事件共享工作空间"></p>
<p align="center"><sub>示意界面预览：主机、路径、用户和模型名称均为虚构数据。</sub></p>

## 熟悉的终端，由你选择的 AI 后端。

保留终端体验，自选背后的智能。OpenWarp 将独立配置的桌面客户端连接到本地 Agent 引擎、你的模型接口，以及本机或受管 SSH 工具。它可以与官方 Warp 同时安装。

| 你的选择 | 获得的能力 |
| :--- | :--- |
| **模型服务** | OpenAI 兼容接口，包括 DeepSeek、Ollama、OpenRouter、LM Studio、vLLM。 |
| **Agent 框架** | Native、Pi Agent 或 DeepSeek Harness；每个 Profile 可独立选择模型。 |
| **本地 + SSH** | 文件搜索、读取、修改、Shell 执行与远程终端上下文。 |
| **命令控制** | 前台/后台执行、输出轮询、输入、取消与退出状态。 |
| **外部 Agent** | 通过本地 MCP 桥接，让 Codex、Claude Code 复用已保存的 SSH 身份。 |
| **原生工作空间** | Agent/终端时间线、排队追问、Steer、SSH 管理与 Quick Paste。 |

## 开始使用

1. 下载 [OpenWarp.app.zip](https://github.com/sasuke39/openwarp/releases/latest/download/OpenWarp.app.zip)，解压并移到 `/Applications`。
2. 打开 **Settings → Agent Engine**，添加接口地址、API Key、模型和 Agent 框架。
3. 进入本机或 SSH 工作空间。提出任务、检查执行过程，或切回直接终端输入。

预编译版本目前支持 **macOS Apple Silicon**，采用开发签名，未经过 Apple 公证。暂未提供完整 Windows 桌面安装包。只有在信任下载来源、且 macOS 拦截时，才移除该应用的隔离属性：

```bash
xattr -cr /Applications/OpenWarp.app
open /Applications/OpenWarp.app
```

## 连接其他编码 Agent

当前客户端源码提供 **Settings → MCP Access**：启用桥接，分别授权每台服务器的命令、上传、下载，再复制 Codex 或 Claude Code 接入配置。若安装版本没有此页面，需要升级或自行构建客户端。使用期间保持 OpenWarp 运行。[MCP 接入说明 →](./docs/external-mcp/setup.md)

关闭 MCP 后拒绝所有新调用，包括查询与取消；已接收命令继续执行。上传/下载开关限制传输工具，不限制已授权 Shell 的文件操作。

## 本地优先，边界清楚。

配置与运行数据保存在本机；提示词与工具上下文会发送到你配置的模型接口。离线使用需要本地模型服务，“本地优先”不意味着远程模型服务接收不到数据。

OpenWarp 是独立社区项目，**与 Warp 官方无隶属关系**。子代理、计算机操作、被动建议与完整 Warp 云端能力尚未实现。

## 技术与开发

[架构图](./docs/public/architecture.svg) · [已支持工具](https://sasuke39.github.io/openwarp/zh/guide/supported-tools) · [配置说明](https://sasuke39.github.io/openwarp/zh/guide/configuration) · [故障排查](https://sasuke39.github.io/openwarp/zh/guide/troubleshooting)

本仓库包含本地适配器与产品文档，桌面源码位于 [openwarp-client](https://github.com/sasuke39/openwarp-client)。打包方式见[构建指南](./WARP_CLIENT.md)。

```bash
go test ./...
WARP_SRC=/path/to/warp-source sh ./build_and_bundle.sh
```

适配器采用 [MIT](./LICENSE)；桌面客户端采用 [AGPL-3.0](https://github.com/sasuke39/openwarp-client/blob/main/LICENSE-AGPL)，其 UI 框架采用 [MIT](https://github.com/sasuke39/openwarp-client/blob/main/LICENSE-MIT)。
