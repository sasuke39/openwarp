# Managed SSH 实现

## 能力分级

| 环境 | Terminal 输入 | Agent 工具 | UI |
|---|---|---|---|
| 本地 | Shell Integration Block | 本地执行器 | 完整统一时间线 |
| Warpified SSH | Remote Block | 独立远程执行器 | 完整统一时间线 |
| 兼容 SSH | 原始 transport PTY | 独立 SSH 通道 | 上 Agent、下 PTY |

## Warpified SSH

- 用户 Shell 命令走当前 Warpified Session，生成真实命令 Block。
- Agent foreground 工具走 Session command executor，返回 stdout/stderr/exit。
- Agent background 工具创建独立 channel/job，支持 poll、input、cancel。
- 用户 PTY 和后台 Agent channel 不互相占用。

## 兼容模式

- 显示“当前环境无法启动 Warpify，已进入兼容模式”和具体原因。
- 上方继续展示 Agent 时间线；下方保留可交互原始 PTY。
- Terminal 输入写入下方 PTY，不解析提示符伪造 Block。
- 提供重试 Warpify 和只读诊断入口。
- 修复 HOME、Shell、tmux 或权限前展示计划；高风险修改重新确认。

## 上下文

- 提交时快照 profile、host、port、user、cwd 和 shell。
- 页面标签、输入 chip、执行器必须引用同一快照来源。
- Agent 工具不得把本机 cwd 当成远端 cwd。
- 切换 SSH 页签后，旧请求仍回到原 Conversation 和环境。

## 安全与回归

- 继续复用 Keychain/AskPass，禁止在日志和 UI 输出密码。
- 拖拽上传复用同一 Profile 认证；有密码时不打开额外终端。
- transport 环境变量和 SSH 启动脚本默认隐藏。
- Warpify 失败不能断开 SSH；重试不能创建重复连接。
