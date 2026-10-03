<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useData, withBase } from 'vitepress'
import { english, chinese } from './copy'
import InteractiveDemo from './demo/InteractiveDemo.vue'
import Architecture from './demo/Architecture.vue'
import CompareVisual from './visuals/CompareVisual.vue'
import SshProof from './visuals/SshProof.vue'
import McpAccess from './visuals/McpAccess.vue'
import StartSteps from './visuals/StartSteps.vue'
import WarpField from './WarpField.vue'
const { page } = useData()
const zh = computed(() => page.value.relativePath.startsWith('zh/'))
const t = computed(() => zh.value ? chinese : english)
const guide = (path: string) => withBase(`${zh.value ? '/zh' : ''}/guide/${path}`)
const release = 'https://github.com/sasuke39/openwarp/releases/latest'
const providers = ['OpenAI', 'DeepSeek', 'Ollama', 'OpenRouter', 'LM Studio', 'vLLM']
const root = ref<HTMLElement>()
let cleanup = () => {}
onMounted(() => {
  const el = root.value!
  const still = matchMedia('(prefers-reduced-motion: reduce)').matches
  // Reveal sections as they enter; content stays visible without JS or with reduced motion.
  let observer: IntersectionObserver | undefined
  if (!still && 'IntersectionObserver' in window) {
    observer = new IntersectionObserver((entries) => {
      for (const entry of entries) if (entry.isIntersecting) { entry.target.classList.add('is-visible'); observer?.unobserve(entry.target) }
    }, { rootMargin: '0px 0px -8% 0px', threshold: 0.08 })
    el.querySelectorAll('.reveal').forEach((node) => observer!.observe(node))
    el.classList.add('reveal-ready')
  }
  // Scroll-linked state: the demo window lands flat as it rises.
  const stage = el.querySelector<HTMLElement>('.demo-stage')
  let frame = 0
  function sync() {
    frame = 0
    const vh = innerHeight
    if (stage) {
      const r = stage.getBoundingClientRect()
      stage.style.setProperty('--land', String(Math.min(1, Math.max(0, (vh - r.top) / (vh * 0.75)))))
    }
  }
  const onScroll = () => { if (!frame) frame = requestAnimationFrame(sync) }
  if (!still) { sync(); addEventListener('scroll', onScroll, { passive: true }); addEventListener('resize', onScroll) }
  else stage?.style.setProperty('--land', '1')
  cleanup = () => { observer?.disconnect(); removeEventListener('scroll', onScroll); removeEventListener('resize', onScroll); cancelAnimationFrame(frame) }
})
onUnmounted(() => cleanup())
</script>

<template>
  <main ref="root" class="product-home" :lang="zh ? 'zh-CN' : 'en'">
    <div class="hero-shell">
      <WarpField class="hero-field" />
      <div class="hero-vignette" aria-hidden="true"></div>
      <div class="hero-layout section-wrap"><section class="hero" aria-labelledby="product-title">
        <div class="hero-meta"><p class="eyebrow hero-pill"><span class="pulse-dot" aria-hidden="true"></span>{{ t.eyebrow }}</p><span class="hero-hud" aria-hidden="true">LOCAL · 127.0.0.1:18888 &nbsp;⟷&nbsp; SSH · *</span></div>
        <h1 id="product-title"><span class="line"><span>{{ zh ? '支持本地与 SSH' : t.title[0] }}</span></span><br><span class="line"><span class="accent-line">{{ zh ? '的 AI 终端' : t.title[1] }}</span><i class="block-cursor" aria-hidden="true"></i></span></h1>
        <div class="hero-foot">
          <p class="hero-intro">{{ t.intro }}</p>
          <div class="hero-cta"><div class="hero-actions"><a class="button primary" :href="release"><svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M16.37 12.6c-.02-2.2 1.8-3.26 1.88-3.31-1.02-1.5-2.62-1.7-3.2-1.73-1.36-.14-2.66.8-3.35.8-.69 0-1.76-.78-2.89-.76-1.49.02-2.86.87-3.63 2.2-1.55 2.68-.4 6.66 1.11 8.84.74 1.07 1.62 2.26 2.77 2.22 1.11-.04 1.53-.72 2.88-.72 1.34 0 1.72.72 2.89.7 1.2-.02 1.96-1.08 2.69-2.15.85-1.24 1.2-2.44 1.22-2.5-.03-.01-2.34-.9-2.37-3.59ZM14.15 6.13c.61-.74 1.03-1.77.91-2.8-.88.04-1.95.59-2.58 1.33-.57.65-1.07 1.7-.93 2.71.98.08 1.99-.5 2.6-1.24Z"/></svg>{{ t.download }} <span aria-hidden="true">↗</span></a><a class="button ghost" :href="guide('getting-started')">{{ t.guide }} <span aria-hidden="true">→</span></a></div><p class="release-note">{{ t.release }}</p></div>
        </div>
      </section></div>
    </div>
    <div class="demo-stage section-wrap"><div class="demo-tilt"><InteractiveDemo /></div></div>
    <div class="provider-band section-wrap"><p class="eyebrow">{{ t.providers }}</p><p class="provider-copy">{{ t.providerCopy }}</p><ul><li v-for="name in providers" :key="name">{{ name }}</li><li class="more">…</li></ul></div>
    <section class="compare-section section-wrap reveal" aria-labelledby="compare-title">
      <div class="section-head"><div><p class="eyebrow">{{ t.compareLabel }}</p><h2 id="compare-title">{{ t.compareTitle }}</h2></div><p class="section-body">{{ t.compareBody }}</p></div>
      <CompareVisual :zh="zh" />
    </section>
    <div class="reveal"><Architecture /></div>
    <section class="ssh-section section-wrap split-section reveal" aria-labelledby="ssh-title">
      <div><p class="eyebrow">{{ t.sshLabel }}</p><h2 id="ssh-title">{{ t.sshTitle }}</h2><p class="section-body">{{ t.sshBody }}</p><ul class="tag-list"><li v-for="tag in t.sshTags" :key="tag">{{ tag }}</li></ul><a class="text-link" :href="guide('supported-tools')">{{ zh ? '了解支持的工具' : 'Explore the tools' }} <span aria-hidden="true">↗</span></a></div>
      <SshProof :zh="zh" />
    </section>
    <section class="mcp-section section-wrap split-section reveal" aria-labelledby="mcp-title">
      <div><p class="eyebrow">{{ t.mcpLabel }}</p><h2 id="mcp-title">{{ t.mcpTitle }}</h2><p class="section-body">{{ t.mcpBody }}</p><ul class="tag-list"><li v-for="tag in t.mcpTags" :key="tag">{{ tag }}</li></ul><a class="text-link" :href="withBase('/external-mcp/setup')">{{ t.mcpLink }} <span aria-hidden="true">↗</span></a></div>
      <McpAccess :zh="zh" />
    </section>
    <section id="get-started" class="start-section section-wrap reveal" aria-labelledby="start-title"><div class="section-head"><div><p class="eyebrow">{{ t.startLabel }}</p><h2 id="start-title">{{ t.startTitle }}</h2></div><a class="button primary" :href="release">{{ t.download }} <span aria-hidden="true">↗</span></a></div><StartSteps :zh="zh" :steps="t.steps" /></section>
    <div class="reveal"><section class="faq-section section-wrap" aria-labelledby="faq-title"><h2 id="faq-title">{{ t.faqTitle }}</h2><div><details v-for="faq in t.faqs" :key="faq[0]"><summary>{{ faq[0] }}<span aria-hidden="true">+</span></summary><p>{{ faq[1] }}</p></details></div></section></div>
    <footer class="product-footer section-wrap"><div><img :src="withBase('/openwarp-icon.png')" alt="" width="32" height="32"><strong>{{ t.closing }}</strong><a href="https://github.com/sasuke39/openwarp">{{ t.github }} ↗</a></div><p>{{ t.footer }}</p><p>{{ t.source }}</p><div class="footer-stage" aria-hidden="true"><WarpField class="footer-field" :intensity="0.7" :cell="12" /><span class="footer-wordmark">OpenWarp</span></div></footer>
  </main>
</template>
