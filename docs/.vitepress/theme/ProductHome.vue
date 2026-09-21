<script setup lang="ts">
import { computed, ref } from 'vue'
import { useData, withBase } from 'vitepress'
import { english, chinese } from './copy'
import agentImage from '../../agent-unified-input/prototypes/warp-complete-agent.png'
import terminalImage from '../../agent-unified-input/prototypes/warp-complete-terminal.png'
const { page } = useData()
const zh = computed(() => page.value.relativePath.startsWith('zh/'))
const t = computed(() => zh.value ? chinese : english)
const guide = (path: string) => withBase(`${zh.value ? '/zh' : ''}/guide/${path}`)
const mode = ref('agent')
const release = 'https://github.com/sasuke39/openwarp/releases/latest'
</script>

<template>
  <main class="product-home" :lang="zh ? 'zh-CN' : 'en'">
    <section class="hero section-wrap" aria-labelledby="product-title">
      <p class="eyebrow"><span class="brand-mark" aria-hidden="true">↗</span>{{ t.eyebrow }}</p>
      <div class="hero-heading"><h1 id="product-title">{{ t.title[0] }}<br><span>{{ t.title[1] }}</span></h1><div class="edition"><span class="eyebrow">{{ t.marker }}</span><p>{{ t.markerText }}</p><span aria-hidden="true">[ OW / 01 ]</span></div></div>
      <p class="hero-intro">{{ t.intro }}</p>
      <div class="hero-actions"><a class="button primary" :href="release">{{ t.download }} <span aria-hidden="true">↗</span></a><a class="text-link" :href="guide('getting-started')">{{ t.guide }} <span aria-hidden="true">→</span></a></div>
      <p class="release-note">{{ t.release }}</p>
    </section>
    <section class="showcase section-wrap" :aria-label="t.preview">
      <div class="preview-toolbar"><span class="eyebrow">OPENWARP / WORKSPACE</span><div class="preview-modes" :aria-label="t.preview" role="group"><button :aria-pressed="mode === 'agent'" @click="mode = 'agent'">{{ t.agent }}</button><button :aria-pressed="mode === 'terminal'" @click="mode = 'terminal'">{{ t.terminal }}</button></div></div>
      <div class="preview-frame"><img :src="mode === 'agent' ? agentImage : terminalImage" :alt="mode === 'agent' ? t.agent : t.terminal" width="1600" height="1000" fetchpriority="high"></div>
      <p class="preview-caption">{{ t.caption }}</p>
    </section>
    <div class="provider-strip section-wrap"><p class="eyebrow">{{ t.providers }}</p><ul><li v-for="name in ['OpenAI', 'DeepSeek', 'Ollama', 'OpenRouter', 'LM Studio', 'vLLM']" :key="name">{{ name }}</li></ul></div>
    <section class="workspace-section section-wrap" aria-labelledby="workspace-title">
      <p class="eyebrow">{{ t.section }}</p><h2 id="workspace-title">{{ t.heading }}</h2>
      <div class="feature-grid"><article v-for="(feature, i) in t.features" :key="feature.tag"><div class="feature-index">0{{ i + 1 }}<span aria-hidden="true">↗</span></div><h3>{{ feature.title }}</h3><p>{{ feature.body }}</p><span class="eyebrow">{{ feature.tag }}</span></article></div>
    </section>
    <section class="remote-section" aria-labelledby="remote-title"><div class="section-wrap split-section">
      <div><p class="eyebrow">{{ t.remoteLabel }}</p><h2 id="remote-title">{{ t.remoteTitle }}</h2><p class="section-body">{{ t.remoteBody }}</p><a class="text-link" :href="guide('supported-tools')">{{ t.remoteLink }} <span aria-hidden="true">↗</span></a></div>
      <figure class="execution-map" :aria-label="t.diagramLabel"><div class="map-node"><span class="eyebrow">{{ t.diagramLocal }}</span><strong>OpenWarp</strong><span>{{ t.diagramEngine }}</span></div><div class="map-connection"><span>SSH</span><span aria-hidden="true">↓</span></div><div class="map-node"><span class="eyebrow">{{ t.diagramRemote }}</span><strong>~/workspace</strong><span>{{ t.diagramTools }}</span></div><figcaption><span class="eyebrow">{{ t.commandTitle }}</span><p>{{ t.command }}</p></figcaption></figure>
    </div></section>
    <section class="mcp-section section-wrap split-section" aria-labelledby="mcp-title"><div><p class="eyebrow">{{ t.mcpLabel }}</p><h2 id="mcp-title">{{ t.mcpTitle }}</h2><p class="section-body">{{ t.mcpBody }}</p><a class="text-link" :href="withBase('/external-mcp/setup')">{{ t.mcpLink }} <span aria-hidden="true">↗</span></a></div><div class="mcp-diagram"><ol><li v-for="(item, i) in t.mcpFlow" :key="item"><span class="node-number">0{{ i + 1 }}</span><strong>{{ item }}</strong><span v-if="i < 2" class="node-arrow" aria-hidden="true">↓</span></li></ol><p>{{ t.mcpNote }}</p></div></section>
    <section id="get-started" class="start-section section-wrap" aria-labelledby="start-title"><div class="start-heading"><div><p class="eyebrow">{{ t.startLabel }}</p><h2 id="start-title">{{ t.startTitle }}</h2></div><a class="button primary" :href="release">{{ t.download }} <span aria-hidden="true">↗</span></a></div><ol class="steps"><li v-for="(step, i) in t.steps" :key="step.title"><span class="eyebrow">0{{ i + 1 }}</span><h3>{{ step.title }}</h3><p>{{ step.text }}</p></li></ol></section>
    <section class="faq-section section-wrap" aria-labelledby="faq-title"><h2 id="faq-title">{{ t.faqTitle }}</h2><div><details v-for="faq in t.faqs" :key="faq[0]"><summary>{{ faq[0] }}<span aria-hidden="true">+</span></summary><p>{{ faq[1] }}</p></details></div></section>
    <footer class="product-footer section-wrap"><div><img :src="withBase('/openwarp-icon.png')" alt="" width="32" height="32"><strong>{{ t.closing }}</strong><a href="https://github.com/sasuke39/openwarp">{{ t.github }} ↗</a></div><p>{{ t.footer }}</p><p>{{ t.source }}</p></footer>
  </main>
</template>
