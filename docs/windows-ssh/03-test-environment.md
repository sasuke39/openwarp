# Windows 测试环境与 UU 操作

## 已验证基线（2026-09-15）

- 控制端：MacBook Pro，Apple M5 Pro，24 GB；不作为 Windows x64 运行证明。
- 测试机：DESKTOP-70KN125，Windows 11 专业版 64 位，AMD64，Intel i5-13400F。
- Windows PATH 找到 Git、Node、OpenSSH；未找到 rustc/cargo/go，不等同于机器绝对未安装。
- Mac UU 4.38.0 build 616，CLI 1.0.0；`uuyc-cli echo` 成功。
- 已通过 UU 窗口执行两条只读检查、观察到输出与下一次 PowerShell 提示符。
- 尚未验证安装包、远程桌面 GUI、构建依赖、文件传输、无人值守或重启恢复。

## UU 的准确用途

- 用 `uuyc-cli device list` 查询设备，以实际返回 ID 匹配指定设备名，不将名称当 ID。
- 本机实际帮助为 `uuyc-cli term open <device-id>`；成功只表示请求打开窗口。
- 此版本没有证实提供远端 exec/stdout API；必须读取窗口确认命令是否执行成功。
- 文章中的 `--shell`、会话管理等参数不得直接假定存在，以安装版本 help 为准。
- UI 操作使用 Computer Use 技能，执行后读取真实窗口；截图不等于可机器解析的测试日志。
- 不通过 UU 私有接口、内存或本地 token 绕过权限；不读取无关家庭文件。
- 长批量测试输出到测试目录日志，由明确授权的文件传输/制品通道取回。
- 若只有手动窗口渠道可用，先以人工辅助运行；不谎称已有全自动远程执行能力。
- 参考：[UU 官方 CLI 文章](https://uuyc.163.com/blog/20260625-cli.html)。

## 隔离与授权

- 安装编译器、Node 更新、启用服务、调整防火墙和上传源码前，分别确认具体动作。
- 先检查可用空间和依赖，不覆盖现有开发工具；优先独立工具路径及测试目录。
- 为 OpenWarp 测试创建独立工作区、配置和临时密钥，不能复用家用机私人 SSH/模型密钥。
- 仓库凭据通过用户授权配置，不将 Mac 凭据复制到 Windows，不把私有源码上传到公共站点。
- 不重启、关机、关闭已有业务进程；不使用无 ID 的批量断连命令。

## 真正的 SSH 测试目标

- 首选 Windows 测试机本地隔离 Linux VM/容器，内部启动临时 sshd，通过回环端口连接。
- VM/容器是否已安装尚未检查；安装与网络变更需要另行确认，不能假定 Docker/WSL 可用。
- WSL 若用于测试基础设施，不意味着产品首版支持本地 WSL 工作区。
- 若环境无法提供 Linux 隔离目标，报告阻塞并安排授权测试环境；不拿生产服务器替代强制测试。
- Mac 侧相关修改仍运行项目隔离本地 sshd E2E；Windows 也必须走实际客户端 Managed SSH 路径。
- UU 连接不是 VPN，不能假设家里 Windows 能直接连接 Mac 的 localhost/局域网地址。
- 仅做 Windows ssh.exe 手工测试不能替代 OpenWarp UI → Adapter → SSH → 工具结果链路。
- 临时 sshd 绑定回环/隔离网段，使用独立端口、密钥与 known_hosts，不扩大公网暴露。

## 自动化与证据

- 优先复用已有真实 SSH 测试和 Harness 契约用例，将环境启动脚本适配 Windows。
- Native/Pi/DSH 必须运行真实进程、实际 Turn、工具结果、完成响应及后续 Turn。
- 可使用确定性本地模型服务，不得用 Mock Harness 冒充实际 Harness。
- 测试记录安装包哈希、两个仓库提交、系统/依赖版本、目标环境、步骤、预期与实际结果。
- 命令日志保留 stdout/stderr/退出码及关联 ID；界面留运行中与完成态原始截图。
- 渲染异常、崩溃、丢输出、超时分别保留证据；不把 UU 网络故障误判成产品故障。
- 测试后清理仅本次创建的进程、密钥与临时服务，保留脱敏证据和可回滚安装包。
