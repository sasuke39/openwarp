<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useInView, wait } from './inView'
const props = defineProps<{ zh: boolean }>()
const root = ref<HTMLElement>()
const { seen, still } = useInView(root)
const command = 'hostname; whoami; pwd'
const tabs = [
  { name: props.zh ? '本地' : 'Local', host: '~/workspace/demo-api', out: ['MacBook-Pro.local', 'dev', '/Users/dev/workspace/demo-api'] },
  { name: 'demo-server', host: 'developer@demo-server', out: ['demo-server', 'developer', '/srv/demo-api'] }
]
const active = ref(1), typed = ref(command), lines = ref(3)
let alive = true
onUnmounted(() => { alive = false })
async function loop() {
  while (alive) {
    for (const tab of [0, 1]) {
      active.value = tab; typed.value = ''; lines.value = 0
      await wait(700)
      for (let i = 1; i <= command.length && alive; i++) { typed.value = command.slice(0, i); await wait(45) }
      await wait(350)
      for (let i = 1; i <= 3 && alive; i++) { lines.value = i; await wait(160) }
      await wait(tab ? 3200 : 1800)
    }
  }
}
watch(seen, (v) => { if (v && !still.value) loop() })
</script>

<template>
  <figure ref="root" class="term">
    <header><span class="window-dots" aria-hidden="true"><i></i><i></i><i></i></span><span v-for="(tab, i) in tabs" :key="tab.name" class="term-tab" :class="{ active: active === i, remote: i === 1 }"><i class="dot"></i>{{ tab.name }}</span></header>
    <div class="term-body">
      <p class="term-host">{{ tabs[active].host }}</p>
      <pre class="term-cmd"><span>$</span> {{ typed }}<i v-if="lines === 0" class="caret"></i></pre>
      <pre class="term-out" :class="{ remote: active === 1 }"><span v-for="(line, i) in tabs[active].out" :key="active + line" :class="{ on: i < lines }">{{ line }}</span></pre>
    </div>
    <figcaption><span :class="{ on: active === 0 }">{{ zh ? '本地页签 → 结果属于你的 Mac' : 'Local tab → your Mac' }}</span><span :class="{ on: active === 1 }">{{ zh ? 'SSH 页签 → 结果属于服务器' : 'SSH tab → the server' }}</span></figcaption>
  </figure>
</template>
