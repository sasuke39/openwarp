<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useData, withBase } from 'vitepress'
import { english, chinese } from './copy'
import InteractiveDemo from './demo/InteractiveDemo.vue'
import Architecture from './demo/Architecture.vue'
import WorkflowPreview from './demo/WorkflowPreview.vue'
import WarpField from './WarpField.vue'
const { page } = useData()
const zh = computed(() => page.value.relativePath.startsWith('zh/'))
const t = computed(() => zh.value ? chinese : english)
const guide = (path: string) => withBase(`${zh.value ? '/zh' : ''}/guide/${path}`)
const release = 'https://github.com/sasuke39/openwarp/releases/latest'
const providers = ['OpenAI', 'DeepSeek', 'Ollama', 'OpenRouter', 'LM Studio', 'vLLM']
// Manifesto lines split into units that light up one by one while the section is pinned.
const manifesto = computed(() => {
  let n = 0
  return t.value.markerText.split('\n').map((line) => (zh.value ? [...line] : line.split(' ')).map((text) => ({ text, i: n++ })))
})
const manifestoCount = computed(() => manifesto.value.flat().length)
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
  // Scroll-linked state: the demo window lands flat as it rises, the manifesto lights word by word.
  const stage = el.querySelector<HTMLElement>('.demo-stage'), pin = el.querySelector<HTMLElement>('.manifesto')
  let frame = 0
  function sync() {
    frame = 0
    const vh = innerHeight
    if (stage) {
      const r = stage.getBoundingClientRect()
      stage.style.setProperty('--land', String(Math.min(1, Math.max(0, (vh - r.top) / (vh * 0.75)))))
    }
    if (pin) {
      const r = pin.getBoundingClientRect()
      pin.style.setProperty('--p', String(Math.min(1, Math.max(0, -r.top / Math.max(1, r.height - vh)))))
    }
  }
  const onScroll = () => { if (!frame) frame = requestAnimationFrame(sync) }
  if (!still) { sync(); addEventListener('scroll', onScroll, { passive: true }); addEventListener('resize', onScroll) }
  else { stage?.style.setProperty('--land', '1'); pin?.style.setProperty('--p', '1') }
  // Cards carry a soft light that follows the pointer.
  const spot = (e: PointerEvent) => {
    const card = (e.target as Element | null)?.closest<HTMLElement>('.architecture-node,.harness-options>div,.architecture-branches>div,.steps li,.mcp-flow li,.faq-section details')
    if (!card) return
    const r = card.getBoundingClientRect()
    card.style.setProperty('--mx', `${e.clientX - r.left}px`); card.style.setProperty('--my', `${e.clientY - r.top}px`)
  }
  el.addEventListener('pointermove', spot, { passive: true })
  cleanup = () => { observer?.disconnect(); removeEventListener('scroll', onScroll); removeEventListener('resize', onScroll); el.removeEventListener('pointermove', spot); cancelAnimationFrame(frame) }
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
        <h1 id="product-title"><span class="line"><span>{{ zh ? '支持本地与 SSH' : t.title[0] }}</span></span><br><span class="line"><span class="glow-text">{{ zh ? '的 AI 终端' : t.title[1] }}</span><i class="block-cursor" aria-hidden="true"></i></span></h1>
        <div class="hero-foot">
          <p class="hero-intro">{{ t.intro }}</p>
          <div class="hero-cta"><div class="hero-actions"><a class="button primary" :href="release"><svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M16.37 12.6c-.02-2.2 1.8-3.26 1.88-3.31-1.02-1.5-2.62-1.7-3.2-1.73-1.36-.14-2.66.8-3.35.8-.69 0-1.76-.78-2.89-.76-1.49.02-2.86.87-3.63 2.2-1.55 2.68-.4 6.66 1.11 8.84.74 1.07 1.62 2.26 2.77 2.22 1.11-.04 1.53-.72 2.88-.72 1.34 0 1.72.72 2.89.7 1.2-.02 1.96-1.08 2.69-2.15.85-1.24 1.2-2.44 1.22-2.5-.03-.01-2.34-.9-2.37-3.59ZM14.15 6.13c.61-.74 1.03-1.77.91-2.8-.88.04-1.95.59-2.58 1.33-.57.65-1.07 1.7-.93 2.71.98.08 1.99-.5 2.6-1.24Z"/></svg>{{ t.download }} <span aria-hidden="true">↗</span></a><a class="button ghost" :href="guide('getting-started')">{{ t.guide }} <span aria-hidden="true">→</span></a></div><p class="release-note">{{ t.release }}</p></div>
        </div>
      </section></div>
    </div>
    <div class="demo-stage section-wrap"><div class="demo-tilt"><InteractiveDemo /></div></div>
    <div class="provider-strip section-wrap"><p class="eyebrow">{{ t.providers }}</p><div class="marquee"><ul><li v-for="name in providers" :key="name">{{ name }}</li></ul><ul aria-hidden="true"><li v-for="name in providers" :key="name">{{ name }}</li></ul></div></div>
    <section class="manifesto" :style="{ '--n': manifestoCount }"><div class="manifesto-pin section-wrap"><p class="eyebrow">{{ t.marker }}</p><p class="manifesto-text" :aria-label="t.markerText.replace('\n', ' ')"><template v-for="(line, li) in manifesto" :key="li"><span class="manifesto-line" aria-hidden="true"><span v-for="unit in line" :key="unit.i" class="manifesto-unit" :style="{ '--i': unit.i }">{{ unit.text }}</span></span></template></p></div></section>
    <div class="section-band reveal"><Architecture /></div>
    <section class="remote-section reveal" aria-labelledby="remote-title"><div class="section-wrap split-section">
      <div><p class="eyebrow">{{ t.remoteLabel }}</p><h2 id="remote-title">{{ t.remoteTitle }}</h2><p class="section-body">{{ t.remoteBody }}</p><dl class="feature-rows"><div><dt>SSH</dt><dd>{{ t.diagramLocal }} → {{ t.diagramRemote }}</dd></div><div><dt>{{ zh ? '远程工具' : 'Remote tools' }}</dt><dd>{{ t.diagramTools }}</dd></div><div><dt>{{ t.commandTitle }}</dt><dd>{{ t.command }}</dd></div></dl><a class="text-link" :href="guide('supported-tools')">{{ t.remoteLink }} <span aria-hidden="true">↗</span></a></div>
      <WorkflowPreview kind="ssh" />
    </div></section>
    <div class="section-band reveal"><section class="mcp-section section-wrap split-section" aria-labelledby="mcp-title"><div><p class="eyebrow">{{ t.mcpLabel }}</p><h2 id="mcp-title">{{ t.mcpTitle }}</h2><p class="section-body">{{ t.mcpBody }}</p><ol class="deployment-steps"><li v-for="(item, i) in (zh ? ['读取服务器日志，定位异常', '在本地修复代码、测试与构建', '通过 MCP 上传并部署', '检查服务状态，读取日志验证'] : ['Read server logs', 'Fix, test, and build locally', 'Upload and deploy through MCP', 'Verify service health and logs'])" :key="item"><span>{{ i + 1 }}</span>{{ item }}</li></ol><a class="text-link" :href="withBase('/external-mcp/setup')">{{ t.mcpLink }} <span aria-hidden="true">↗</span></a></div><div class="mcp-details"><ol class="mcp-flow"><li v-for="(item, i) in t.mcpFlow" :key="item"><span class="node-number">0{{ i + 1 }}</span><strong>{{ item }}</strong><span v-if="i < 2" class="node-arrow" aria-hidden="true">↓</span></li></ol><p>{{ t.mcpNote }}</p><WorkflowPreview kind="mcp" /></div></section></div>
    <section id="get-started" class="start-section section-wrap reveal" aria-labelledby="start-title"><div class="start-heading"><div><p class="eyebrow">{{ t.startLabel }}</p><h2 id="start-title">{{ t.startTitle }}</h2></div><a class="button primary" :href="release">{{ t.download }} <span aria-hidden="true">↗</span></a></div><ol class="steps"><li v-for="(step, i) in t.steps" :key="step.title"><span class="step-number" aria-hidden="true">0{{ i + 1 }}</span><h3>{{ step.title }}</h3><p>{{ step.text }}</p></li></ol></section>
    <div class="section-band reveal"><section class="faq-section section-wrap" aria-labelledby="faq-title"><h2 id="faq-title">{{ t.faqTitle }}</h2><div><details v-for="faq in t.faqs" :key="faq[0]"><summary>{{ faq[0] }}<span aria-hidden="true">+</span></summary><p>{{ faq[1] }}</p></details></div></section></div>
    <footer class="product-footer section-wrap"><div><img :src="withBase('/openwarp-icon.png')" alt="" width="32" height="32"><strong>{{ t.closing }}</strong><a href="https://github.com/sasuke39/openwarp">{{ t.github }} ↗</a></div><p>{{ t.footer }}</p><p>{{ t.source }}</p><div class="footer-stage" aria-hidden="true"><WarpField class="footer-field" :intensity="0.7" :cell="12" /><span class="footer-wordmark">OpenWarp</span></div></footer>
  </main>
</template>
