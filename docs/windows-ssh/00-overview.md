# Windows x64 SSH 首版技术方案

状态：设计草案；仅完成代码初查、UU 连通性验证，尚未实现或验证 Windows OpenWarp。

## 目标与范围

- 发布 Windows x64 客户端，连接远端 Linux，提供交互式 SSH 与内置 Agent。
- 复用现有 Rust UI、Go Adapter、Native/Pi/DSH、工具协议与对话生命周期。
- 支持远端普通命令、文件工具、显式后台任务、输出读取、输入、取消和拖放上传。
- Windows 不开放本地终端和本地 Agent 工作区；模型请求与 Adapter 仍在 Windows 本机运行。
- 不重写终端引擎，不把 Agent CLI 安装到远端，不以 UU 代替产品 SSH 协议。
- 首版不承诺 Windows ARM、远端 Windows、WSL 工作区或复杂交互堡垒机。
- 外部 MCP Windows 入口建议后置，待确认；必须显式标为不可用，不留下可点击的失效入口。
- macOS 已有功能、配置和发布流程不得退化。

## 文档索引

1. [架构、执行与安全边界](01-architecture.md)
2. [改造点、源码定位与构建](02-implementation.md)
3. [Windows 测试环境与 UU 操作](03-test-environment.md)
4. [实施 TODO、验收与回滚](04-delivery.md)

## 当前事实

| 项目 | 已确认事实 | 尚未确认 |
|---|---|---|
| Warp 上游 | 有 Windows 平台代码、x64/ARM64 构建与安装脚本 | 定制版本能否编译启动 |
| SSH 执行器 | 独立启动系统 ssh/sftp，捕获输出 | Windows 下认证、参数与取消 |
| 密码认证 | 当前取密码函数在非 macOS 返回不支持；辅助脚本依赖 sh | Windows 原生辅助程序 |
| 外部 MCP | 客户端入口受 unix 条件限制，Go 通信使用 Unix Socket | Windows 通信实现 |
| 测试设备 | DESKTOP-70KN125，Windows 11 专业版 x64，i5-13400F | 内存、磁盘、构建依赖、图形兼容性 |
| UU | 实测打开远程 PowerShell、执行只读命令、截图读取输出成功 | 非交互命令接口、文件传输及 GUI 测试稳定性 |

## 实施原则

- 共享业务逻辑，平台差异收敛到启动、凭据、路径、终端宿主和打包边界。
- 不复制一套 Windows Turn/工具状态机，不使用运行时间自动推断后台模式。
- SSH 断线或上下文缺失必须失败，禁止静默回退到 Windows 本机执行。
- 分清“可编译”“可启动”“真实 SSH 通过”“真实 GUI 验收”，分别留证据。
- 先做 x64 编译和最小链路探索，再估算工作量；当前不承诺固定工期。
- 此文档不授权安装软件、修改家用电脑设置、公开源码或发布安装包。
