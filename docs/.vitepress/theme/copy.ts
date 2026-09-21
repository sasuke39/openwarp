export const english = {
  eyebrow: 'THE LOCAL-FIRST AI TERMINAL', title: ['Your terminal.', 'Your intelligence.'],
  intro: 'Bring your own models. Work across local projects and SSH. Keep your coding agents connected to the tools you already use.',
  download: 'Download for Mac', guide: 'Read the guide', release: 'Apple Silicon · Development-signed build',
  marker: 'BUILT FOR THE WAY YOU WORK', markerText: 'A familiar terminal.\nA backend of your own.',
  preview: 'A closer look', agent: 'Agent workflow', terminal: 'Terminal mode',
  caption: 'Illustrative UI preview · fictional hosts, paths, and model names.', providers: 'YOUR ENDPOINT. YOUR CHOICE.',
  section: '01 / THE WORKSPACE', heading: 'Less switching.\nMore getting things done.',
  features: [
    { title: 'Choose your intelligence.', body: 'Connect an OpenAI-compatible endpoint and choose Native, Pi Agent, or DeepSeek Harness. Models and runtimes are your decision.', tag: 'MODELS + HARNESSES' },
    { title: 'Stay in the conversation.', body: 'Agent actions and terminal output share a timeline. Queue a follow-up, steer a running Turn, or take direct shell control.', tag: 'AGENT + TERMINAL' },
    { title: 'Make it your workspace.', body: 'Manage SSH connections, model profiles, and reusable Quick Paste snippets inside the desktop app. Keep configuration on your machine.', tag: 'NATIVE CONTROLS' }
  ],
  remoteLabel: '02 / BEYOND LOCALHOST', remoteTitle: 'Remote work.\nSame control.',
  remoteBody: 'An SSH connection is more than a shell tab. Give your agent remote context and a managed command lifecycle: run, inspect output, provide input, or cancel.',
  remoteLink: 'Explore the tools', diagramLabel: 'Execution architecture', diagramLocal: 'YOUR MACHINE', diagramEngine: 'Local Agent Engine', diagramRemote: 'SSH SERVER', diagramTools: 'Files / Shell / Processes', commandTitle: 'COMMAND LIFECYCLE', command: 'Run → Poll → Input / Cancel → Exit status',
  mcpLabel: '03 / CONNECT YOUR AGENTS', mcpTitle: 'Your servers.\nYour favorite agent.',
  mcpBody: 'Use saved SSH connections from Codex, Claude Code, or another local MCP client. The desktop app owns the credentials; external agents use the tools.',
  mcpLink: 'Set up MCP', mcpNote: 'MCP Access in current client source: command, upload, and download permissions per server. Availability depends on your installed build.',
  mcpFlow: ['Codex / Claude Code', 'OpenWarp · MCP bridge', 'Your saved SSH connections'],
  startLabel: '04 / GET STARTED', startTitle: 'Make room for\nyour next idea.',
  steps: [
    { title: 'Install OpenWarp.', text: 'Download the Mac app from GitHub Releases. It can live alongside official Warp.' },
    { title: 'Bring your model.', text: 'Add an endpoint, API key, and model in Settings → Agent Engine.' },
    { title: 'Open a workspace.', text: 'Start locally or over SSH. Ask your agent, inspect its work, and stay in control.' }
  ],
  faqTitle: 'Before you begin.', faqs: [
    ['Which platforms are available?', 'Prebuilt releases currently target macOS on Apple Silicon. A complete Windows desktop installer is not available yet.'],
    ['Does local-first mean offline?', 'Configuration and runtime state live locally. Prompts and tool context go to your configured model endpoint. Offline use requires a local model service.'],
    ['Is this an official Warp product?', 'No. OpenWarp is an independent community project built around the open-source Warp client. It is not affiliated with Warp.'],
    ['What is not supported yet?', 'Full Warp cloud parity, subagents, computer use, and passive suggestions are not implemented. Check the supported-tools guide for the current scope.']
  ],
  closing: 'Open source. Open choices.', github: 'Explore on GitHub', footer: 'Independent community project. Not affiliated with Warp.', source: 'Adapter: MIT · Client: AGPL-3.0 / MIT'
}

export const chinese: typeof english = {
  eyebrow: '本地优先的智能终端', title: ['你的终端，', '你的 AI。'],
  intro: '模型自己选，本地与 SSH 工作流自由切换。让你熟悉的编码 Agent，连接你已经在用的工具。',
  download: '下载 Mac 版', guide: '阅读使用指南', release: 'Apple Silicon · 开发签名版本',
  marker: '为真实的开发工作而构建', markerText: '熟悉的终端。\n由你选择的 AI 后端。',
  preview: '看看工作界面', agent: 'Agent 工作流', terminal: '终端模式', caption: '示意界面预览 · 主机、路径和模型名称均为虚构数据。',
  providers: '你的接口，你的选择。', section: '01 / 工作空间', heading: '少一些切换，\n多一些专注。',
  features: [
    { title: '选择适合你的 AI。', body: '连接 OpenAI 兼容接口，在 Native、Pi Agent 和 DeepSeek Harness 之间选择。模型与运行框架，都由你决定。', tag: '模型 + AGENT 框架' },
    { title: '让工作保持连贯。', body: 'Agent 操作与终端输出共享时间线。追加下一轮任务、引导进行中的 Turn，或直接接管 Shell。', tag: 'AGENT + 终端' },
    { title: '把工具变成自己的。', body: '在原生应用中管理 SSH 连接、模型配置和 Quick Paste 常用片段。配置与运行数据留在本机。', tag: '原生管理界面' }
  ],
  remoteLabel: '02 / 不止于本机', remoteTitle: '远程工作，\n一样掌控。', remoteBody: 'SSH 不只是一个 Shell 标签页。让 Agent 获得远程上下文，并使用受管命令生命周期：执行、读取输出、传入输入，或取消任务。',
  remoteLink: '了解支持的工具', diagramLabel: '执行架构', diagramLocal: '本地电脑', diagramEngine: '本地 Agent 引擎', diagramRemote: 'SSH 服务器', diagramTools: '文件 / Shell / 进程', commandTitle: '命令生命周期', command: '执行 → 轮询 → 输入 / 取消 → 退出状态',
  mcpLabel: '03 / 连接你的 AGENT', mcpTitle: '你的服务器，\n熟悉的 Agent。', mcpBody: '让 Codex、Claude Code 或其他本地 MCP 客户端复用已保存的 SSH 连接。凭据由桌面应用管理，外部 Agent 使用工具。',
  mcpLink: '配置 MCP 接入', mcpNote: '当前客户端源码提供 MCP Access，可分别授权每台服务器的命令、上传和下载。功能是否可用取决于安装的版本。', mcpFlow: ['Codex / Claude Code', 'OpenWarp · MCP 桥接', '已保存的 SSH 连接'],
  startLabel: '04 / 开始使用', startTitle: '为下一个想法，\n打开终端。', steps: [
    { title: '安装 OpenWarp。', text: '从 GitHub Releases 下载 Mac 应用，可与官方 Warp 同时安装。' },
    { title: '连接你的模型。', text: '在 Settings → Agent Engine 添加接口地址、API Key 和模型。' },
    { title: '打开工作空间。', text: '从本机或 SSH 开始。提出任务、检查执行过程，随时接管操作。' }
  ],
  faqTitle: '开始之前。', faqs: [
    ['目前支持哪些系统？', '预编译版本目前支持 Apple Silicon 芯片的 macOS。暂未提供完整的 Windows 桌面安装包。'],
    ['本地优先，意味着完全离线吗？', '配置与运行数据保存在本机；提示词和工具上下文会发送到你配置的模型接口。离线使用需要本地模型服务。'],
    ['这是 Warp 官方产品吗？', '不是。OpenWarp 是围绕开源 Warp 客户端构建的独立社区项目，与 Warp 官方没有隶属关系。'],
    ['哪些能力还没有实现？', '尚未实现完整 Warp 云端能力、子代理、计算机操作和被动建议。具体范围请查阅已支持工具文档。']
  ],
  closing: '代码开放，选择自由。', github: '在 GitHub 查看源码', footer: '独立社区项目，与 Warp 官方无隶属关系。', source: '适配器：MIT · 客户端：AGPL-3.0 / MIT'
}
