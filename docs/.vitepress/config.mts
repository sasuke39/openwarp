import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'OpenWarp',
  description: 'AI terminal for local and SSH work. Connect coding agents to your servers through MCP.',
  appearance: false,
  base: '/openwarp/',
  cleanUrls: true,
  locales: {
    root: { label: 'English', lang: 'en' },
    zh: {
      label: '简体中文', lang: 'zh-CN',
      themeConfig: { nav: [
        { text: '使用指南', link: '/zh/guide/getting-started' },
        { text: '工具能力', link: '/zh/guide/supported-tools' }
      ] }
    }
  },
  head: [
    ['meta', { name: 'theme-color', content: '#f8f7f3' }],
    ['link', { rel: 'icon', type: 'image/png', href: '/openwarp/openwarp-icon.png' }],
    ['meta', { property: 'og:title', content: 'OpenWarp — AI terminal for local and SSH work' }],
    ['meta', { property: 'og:description', content: 'Choose your models. Work locally or over SSH. Connect your coding agents through MCP.' }]
  ],
  themeConfig: {
    logo: '/openwarp-icon.png',
    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Tools', link: '/guide/supported-tools' }
    ],
    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Getting Started', link: '/guide/getting-started' },
            { text: 'Configuration', link: '/guide/configuration' },
            { text: 'Warp Client', link: '/guide/warp-client' },
            { text: 'Supported Tools', link: '/guide/supported-tools' },
            { text: 'Troubleshooting', link: '/guide/troubleshooting' }
          ]
        }
      ],
      '/zh/guide/': [
        {
          text: '指南',
          items: [
            { text: '快速开始', link: '/zh/guide/getting-started' },
            { text: '配置说明', link: '/zh/guide/configuration' },
            { text: 'Warp 客户端', link: '/zh/guide/warp-client' },
            { text: '已支持工具', link: '/zh/guide/supported-tools' },
            { text: '故障排查', link: '/zh/guide/troubleshooting' }
          ]
        }
      ]
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/sasuke39/openwarp' }
    ],
    search: {
      provider: 'local'
    }
  }
})
