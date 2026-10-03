<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { withBase } from 'vitepress'
import { useInView, wait } from './inView'
const props = defineProps<{ zh: boolean; steps: { title: string; text: string; where: string }[] }>()
const root = ref<HTMLElement>()
const { seen, still } = useInView(root)
const L = computed(() => props.zh
  ? { apps: 'Applications', installed: '已安装', provider: '服务商', key: 'API Key', test: '测试连接', ok: '已连接 · 142 ms', local: '本地', ask: '帮我看看 demo-api 为什么返回 500', reading: '读取日志', found: '找到原因：orders.go:42 customer 为空' }
  : { apps: 'Applications', installed: 'Installed', provider: 'Provider', key: 'API Key', test: 'Test connection', ok: 'Connected · 142 ms', local: 'Local', ask: 'Why is demo-api returning 500?', reading: 'Reading logs', found: 'Root cause: orders.go:42, customer is nil' })
const url = 'https://api.deepseek.com/v1'
// step: which column is playing; sub-states drive each mock.
const step = ref(-1), moved = ref(false), installed = ref(false), endpoint = ref(''), tested = ref(0), tab = ref(0), reply = ref(0)
let alive = true
onUnmounted(() => { alive = false })
function finalState() { step.value = 3; moved.value = true; installed.value = true; endpoint.value = url; tested.value = 2; tab.value = 1; reply.value = 3 }
async function loop() {
  while (alive) {
    step.value = 0; moved.value = false; installed.value = false; endpoint.value = ''; tested.value = 0; tab.value = 0; reply.value = 0
    await wait(500); moved.value = true; await wait(900); installed.value = true; await wait(700)
    step.value = 1
    for (let i = 1; i <= url.length && alive; i++) { endpoint.value = url.slice(0, i); await wait(32) }
    await wait(300); tested.value = 1; await wait(800); tested.value = 2; await wait(800)
    step.value = 2; await wait(500); tab.value = 1
    for (let i = 1; i <= 3 && alive; i++) { await wait(i === 1 ? 600 : 900); reply.value = i }
    await wait(800); step.value = 3; await wait(3800)
  }
}
watch(seen, (v) => { if (!v) return; if (still.value) finalState(); else loop() })
const progress = computed(() => step.value < 0 ? 0 : Math.min(100, (step.value + (step.value < 3 ? .5 : 0)) / 3 * 100))
</script>

<template>
  <div ref="root" class="start">
    <div class="start-progress" aria-hidden="true"><i :style="{ width: progress + '%' }"></i></div>
    <ol>
      <li v-for="(s, i) in steps" :key="s.title" :class="{ active: step === i, done: step > i }">
        <header><span class="start-num">0{{ i + 1 }}</span><h3>{{ s.title }}</h3><span class="start-where">{{ s.where }}</span></header>
        <div class="start-visual" aria-hidden="true">
          <div v-if="i === 0" class="mock-install">
            <div class="slot app" :class="{ moved }"><img :src="withBase('/openwarp-icon.png')" alt="" width="44" height="44"><span>OpenWarp</span></div>
            <svg class="arrow" width="48" height="12" viewBox="0 0 48 12"><path d="M0 6h44M38 1l6 5-6 5" /></svg>
            <div class="slot folder" :class="{ filled: moved }"><svg width="48" height="40" viewBox="0 0 48 40"><path d="M2 6a3 3 0 0 1 3-3h12l4 5h22a3 3 0 0 1 3 3v24a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3z" /></svg><span>{{ L.apps }}</span></div>
            <p class="install-state" :class="{ on: installed }">✓ {{ L.installed }}</p>
          </div>
          <div v-else-if="i === 1" class="mock-settings">
            <div class="field"><label>{{ L.provider }}</label><p>DeepSeek <i>▾</i></p></div>
            <div class="field"><label>Base URL</label><p>{{ endpoint }}<i v-if="step === 1 && tested === 0" class="caret"></i></p></div>
            <div class="field"><label>{{ L.key }}</label><p class="secret">sk-••••••••••3f2a</p></div>
            <div class="test"><span class="btn" :class="{ busy: tested === 1 }">{{ L.test }}</span><em :class="{ on: tested === 2 }">✓ {{ L.ok }}</em></div>
          </div>
          <div v-else class="mock-agent">
            <div class="tabs"><span :class="{ on: tab === 0 }">{{ L.local }}</span><span :class="{ on: tab === 1 }"><i class="dot"></i>demo-server</span></div>
            <p class="ask"><b>❯</b>{{ L.ask }}</p>
            <ul>
              <li :class="{ on: reply >= 1 }"><span class="spin">◌</span>{{ L.reading }} <code>journalctl -u demo-api</code></li>
              <li :class="{ on: reply >= 2 }"><span>›</span><code>ERROR POST /orders 500</code></li>
              <li :class="{ on: reply >= 3 }" class="found"><span>✓</span>{{ L.found }}</li>
            </ul>
          </div>
        </div>
      </li>
    </ol>
  </div>
</template>
