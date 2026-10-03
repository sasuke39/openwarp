export const english = {
  eyebrow: 'OPENWARP', title: ['AI terminal for', 'local and SSH work.'],
  intro: 'Run an Agent locally to work on your projects and SSH servers. Connect Codex or Claude Code through MCP to read logs, upload builds, and deploy.',
  download: 'Download for Mac', guide: 'Documentation', release: 'macOS · Apple Silicon',
  marker: 'BUILT FOR THE WAY YOU WORK', markerText: 'A familiar terminal.\nA backend of your own.',
  preview: 'A closer look', agent: 'Agent workflow', terminal: 'Terminal mode',
  caption: 'Illustrative UI preview · fictional hosts, paths, and model names.', providers: 'MODEL PROVIDERS',
  section: '01 / THE WORKSPACE', heading: 'Less switching.\nMore getting things done.',
  features: [
    { title: 'Choose your intelligence.', body: 'Connect an OpenAI-compatible endpoint and choose Native, Pi Agent, or DeepSeek Harness. Models and runtimes are your decision.', tag: 'MODELS + HARNESSES' },
    { title: 'Stay in the conversation.', body: 'Agent actions and terminal output share a timeline. Queue a follow-up, steer a running Turn, or take direct shell control.', tag: 'AGENT + TERMINAL' },
    { title: 'Make it your workspace.', body: 'Manage SSH connections, model profiles, and reusable Quick Paste snippets inside the desktop app. Keep configuration on your machine.', tag: 'NATIVE CONTROLS' }
  ],
  providerCopy: 'Works with any OpenAI-compatible endpoint. API keys stay on your machine.',
  compareLabel: '01 / WHY OPENWARP', compareTitle: 'No Agent to install\non every server.',
  compareBody: 'Your Mac carries the Agent and the keys. Servers only need SSH.',
  sshLabel: '03 / SSH AGENT', sshTitle: 'Commands run on the server.\nAlways.',
  sshBody: 'Start the Agent in an SSH tab and every command runs on that host.',
  sshTags: ['Dedicated SSH channel', 'Never falls back to your Mac', 'Passwords via Keychain'],
  mcpLabel: '04 / SSH MCP', mcpTitle: 'Let Codex use your SSH\nconnections. Server by server.',
  mcpBody: 'Codex and Claude Code use your saved connections. They only get what you switch on.',
  mcpTags: ['Off by default', 'No passwords or keys exposed', 'Unknown host keys rejected'],
  mcpLink: 'Set up MCP',
  startLabel: '05 / GET STARTED', startTitle: 'Three steps.',
  steps: [
    { title: 'Install OpenWarp', text: 'Download the Mac app. It can live alongside official Warp.', where: 'GitHub Releases' },
    { title: 'Connect a model', text: 'Add an endpoint, API key, and model.', where: 'Settings → Agent Engine' },
    { title: 'Open a workspace', text: 'Ask the Agent in a local tab, or enable it in an SSH tab.', where: 'Local tab / SSH tab' }
  ],
  faqTitle: 'Frequently asked questions', faqs: [
    ['Which platforms are available?', 'Prebuilt releases currently target macOS on Apple Silicon. A complete Windows desktop installer is not available yet.'],
    ['Does local-first mean offline?', 'Configuration and runtime state live locally. Prompts and tool context go to your configured model endpoint. Offline use requires a local model service.'],
    ['Is this an official Warp product?', 'No. OpenWarp is an independent community project built around the open-source Warp client. It is not affiliated with Warp.'],
    ['What is not supported yet?', 'Full Warp cloud parity, subagents, computer use, and passive suggestions are not implemented. Check the supported-tools guide for the current scope.']
  ],
  closing: 'OpenWarp', github: 'Source on GitHub', footer: 'Independent community project. Not affiliated with Warp.', source: 'Adapter: MIT · Client: AGPL-3.0 / MIT'
}

export const chinese: typeof english = {
  eyebrow: 'OPENWARP', title: ['支持本地与 SSH 的', 'AI 终端'],
  intro: '在本地运行 Agent，处理项目代码和远程服务器。也可以通过 MCP，让 Codex、Claude Code 直接读取日志、上传文件和部署服务。',
  download: '下载 Mac 版', guide: '使用文档', release: 'macOS · Apple Silicon',
  marker: '为真实的开发工作而构建', markerText: '熟悉的终端。\n由你选择的 AI 后端。',
  preview: '看看工作界面', agent: 'Agent 工作流', terminal: '终端模式', caption: '示意界面预览 · 主机、路径和模型名称均为虚构数据。',
  providers: '支持的模型服务', section: '01 / 工作空间', heading: '少一些切换，\n多一些专注。',
  features: [
    { title: '选择适合你的 AI。', body: '连接 OpenAI 兼容接口，在 Native、Pi Agent 和 DeepSeek Harness 之间选择。模型与运行框架，都由你决定。', tag: '模型 + AGENT 框架' },
    { title: '让工作保持连贯。', body: 'Agent 操作与终端输出共享时间线。追加下一轮任务、引导进行中的 Turn，或直接接管 Shell。', tag: 'AGENT + 终端' },
    { title: '把工具变成自己的。', body: '在原生应用中管理 SSH 连接、模型配置和 Quick Paste 常用片段。配置与运行数据留在本机。', tag: '原生管理界面' }
  ],
  providerCopy: '接入任意 OpenAI 兼容接口，API Key 只保存在本机。',
  compareLabel: '01 / 为什么用 OPENWARP', compareTitle: '不用在每台服务器上\n装一遍 Agent。',
  compareBody: 'Agent 和密钥都在你的 Mac 上，服务器只需要 SSH。',
  sshLabel: '03 / SSH AGENT', sshTitle: '命令一定在服务器上执行。',
  sshBody: '在 SSH 页签里启用 Agent，每条命令都在那台服务器上跑。',
  sshTags: ['独立 SSH 执行通道', '绝不回退到本机', '密码只走 Keychain'],
  mcpLabel: '04 / SSH MCP', mcpTitle: '让 Codex 用你的 SSH 连接，\n权限逐台给。',
  mcpBody: 'Codex、Claude Code 用你已保存的连接，只能用你打开的权限。',
  mcpTags: ['默认不授权', '不暴露密码和私钥', '拒绝未知主机密钥'],
  mcpLink: '配置 MCP 接入',
  startLabel: '05 / 开始使用', startTitle: '三步开始。',
  steps: [
    { title: '安装 OpenWarp', text: '下载 Mac 应用，可与官方 Warp 同时安装。', where: 'GitHub Releases' },
    { title: '连接模型', text: '填写接口地址、API Key 和模型名称。', where: 'Settings → Agent Engine' },
    { title: '打开工作空间', text: '在本地页签直接提问，或在 SSH 页签启用 Agent。', where: '本地页签 / SSH 页签' }
  ],
  faqTitle: '常见问题', faqs: [
    ['目前支持哪些系统？', '预编译版本目前支持 Apple Silicon 芯片的 macOS。暂未提供完整的 Windows 桌面安装包。'],
    ['本地优先，意味着完全离线吗？', '配置与运行数据保存在本机；提示词和工具上下文会发送到你配置的模型接口。离线使用需要本地模型服务。'],
    ['这是 Warp 官方产品吗？', '不是。OpenWarp 是围绕开源 Warp 客户端构建的独立社区项目，与 Warp 官方没有隶属关系。'],
    ['哪些能力还没有实现？', '尚未实现完整 Warp 云端能力、子代理、计算机操作和被动建议。具体范围请查阅已支持工具文档。']
  ],
  closing: 'OpenWarp', github: 'GitHub 源码', footer: '独立社区项目，与 Warp 官方无隶属关系。', source: '适配器：MIT · 客户端：AGPL-3.0 / MIT'
}
