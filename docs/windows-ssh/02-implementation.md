# 改造点、源码定位与构建

本文路径相对工作区；U 表示 `warp-v0.2026.04.29.08.56.stable_00-src/warp-0.2026.04.29.08.56.stable_00`。

## 修改地图

| 位置 | 当前情况 | 计划 |
|---|---|---|
| U/app/src/terminal/ssh/credentials.rs | sh askpass；非 macOS 取密码报错 | Windows 原生 helper，复用 secure_storage，认证错误可见 |
| U/app/src/terminal/ssh/connection.rs | 交互连接含 Unix 环境变量前缀 | 分离连接参数、环境、显示文本，通过平台启动接口接入 |
| U/app/src/terminal/ssh/managed_command_executor.rs | 直接启动 ssh/sftp | 可信程序定位、参数与路径、输出/取消 Windows 回归 |
| U/app/src/terminal/ssh/connection_manager.rs | 管理连接状态 | 复用状态，仅接平台差异；断线不得回退本地 |
| U/app/src/terminal/ssh/warpify.rs、install_tmux.rs | 远端握手和安装 | 复用 Linux 逻辑，验证 Windows PTY 的协议传输 |
| U/app/src/terminal/ssh/tool_ipc/ | Unix 专属通信与权限 | 首版关闭 Windows 外部 MCP；后续独立适配 |
| local-adapter/internal/agent/ | Agent 上下文 | 区分宿主 Windows 与执行目标 Linux，拒绝本地执行 |
| local-adapter/cmd/server/ 与 Harness 启动模块 | 本地服务、子进程、运行时定位 | .exe 路径、工作目录、重启、进程清理与端口冲突 |
| U/script/windows/ | 上游构建和 Inno Setup 配置 | 扩展 OpenWarp local 通道产物，不另造平行安装体系 |
| local-adapter/build_and_bundle.sh | macOS .app 打包 | 保持不变；增加对应 Windows 编排脚本与共同版本清单 |

## 代码组织

- 先检索上游 Windows 进程、凭据、ConPTY、资源目录接口，再补缺口。
- Rust 使用平台模块/cfg，Go 使用平台文件/build tags，避免大量散落条件判断。
- 保持现有工具 Schema 与 ID 含义；如必须修改协议，应同步客户端、三个 Harness 和测试。
- 禁止为了 Windows 删除 macOS 功能；新增公共行为同时跑 macOS 回归。
- 实现前读取目标目录 AGENTS，创建安全开发分支；文档不表示已授权修改上游源码。

## 构建环境

- 第一目标：`x86_64-pc-windows-msvc`；Go 使用 windows/amd64；Node 与依赖均为 Windows x64。
- Rust 按仓库 toolchain 锁定；MSVC、Windows SDK、链接依赖按上游脚本与实际构建补齐。
- 上游 install_build_deps.ps1 自身调用 bash：构建机可能需要 Git Bash；最终用户不应因此必须安装 Bash。
- Go、Node、npm 版本依据模块/锁文件，不能因测试机已有 Node 就直接认定满足 Harness 要求。
- 检查 Pi/DSH 原生依赖与可选依赖在 Windows 的安装结果，不复制 macOS node_modules。
- 先 cargo check 和 Go 编译，再完整链接、资源打包；所有错误保留完整日志。
- 第三方下载、许可证、签名证书与构建工具安装分别确认；不关闭杀毒软件或系统安全策略。

## 安装包与运行时

- 最终包包含客户端、Adapter、受支持 Node、三个 Harness 与资源/版本清单。
- 系统 ssh/sftp 缺失时明确提示安装前置；是否随包分发 OpenSSH 后续按许可与体积决定。
- 配置、日志与会话数据使用平台路径 API；不写 Program Files、不硬编码 macOS 路径。
- 保留现有会话存储策略，本任务不额外迁移 Pi/DSH Session 根目录。
- 应用从资源目录启动子进程，不依赖开发者 PATH；验证含空格/中文安装目录。
- 预览包与正式版本身份、运行目录、端口隔离；单实例/端口占用不能误连其他 Adapter。
- 生成源提交、工具链、依赖版本、SHA-256 清单；区分签名发布与未签名测试包。
- 外部 MCP 后续可评估 Named Pipe 与当前用户 ACL；不以无认证公网/全网监听 TCP 替代。

## 首次探索输出

- 精确列出能复用、需修改、被依赖阻塞的模块；给出实际 Windows 编译错误清单。
- 完成最小链路后再估算工作量与剩余风险，不用文件数或单测数代替可用性。
