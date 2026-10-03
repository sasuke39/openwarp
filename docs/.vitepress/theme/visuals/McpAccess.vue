<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useInView, wait } from './inView'
const props = defineProps<{ zh: boolean }>()
const root = ref<HTMLElement>()
const { seen, still } = useInView(root)
const L = props.zh
  ? { title: 'Settings → MCP Access', enable: '启用 MCP', server: '服务器', cols: ['命令', '上传', '下载'], log: '调用日志', allow: '已允许', deny: '未授权，已拒绝', copy: '复制接入配置' }
  : { title: 'Settings → MCP Access', enable: 'Enable MCP', server: 'Server', cols: ['Commands', 'Upload', 'Download'], log: 'Call log', allow: 'allowed', deny: 'not granted, rejected', copy: 'Copy config' }
const target = [[true, true, false], [true, false, false], [false, false, false]]
const servers = ['demo-server', 'staging-db', 'prod-api']
const grid = ref(target.map((r) => r.map(() => false))), enabled = ref(false)
const calls = [
  { tool: 'workspace_shell', server: 'demo-server', ok: true },
  { tool: 'sftp_upload', server: 'demo-server', ok: true },
  { tool: 'sftp_upload', server: 'staging-db', ok: false },
  { tool: 'workspace_shell', server: 'prod-api', ok: false }
]
const shown = ref(0)
let alive = true
onUnmounted(() => { alive = false })
function finalState() { enabled.value = true; grid.value = target.map((r) => [...r]); shown.value = calls.length }
async function loop() {
  while (alive) {
    enabled.value = false; grid.value = target.map((r) => r.map(() => false)); shown.value = 0
    await wait(600); enabled.value = true; await wait(500)
    for (let r = 0; r < 3; r++) for (let c = 0; c < 3; c++) if (target[r][c] && alive) { grid.value[r][c] = true; await wait(380) }
    await wait(400)
    for (let i = 1; i <= calls.length && alive; i++) { shown.value = i; await wait(900) }
    await wait(4000)
  }
}
watch(seen, (v) => { if (!v) return; if (still.value) finalState(); else loop() })
</script>

<template>
  <figure ref="root" class="access">
    <header><span class="window-dots" aria-hidden="true"><i></i><i></i><i></i></span><span>{{ L.title }}</span></header>
    <div class="access-toggle"><strong>{{ L.enable }}</strong><i class="switch" :class="{ on: enabled }"></i></div>
    <div class="access-grid" role="table">
      <div class="access-row head" role="row"><span role="columnheader">{{ L.server }}</span><span v-for="c in L.cols" :key="c" role="columnheader">{{ c }}</span></div>
      <div v-for="(s, r) in servers" :key="s" class="access-row" role="row"><span role="rowheader">{{ s }}</span><span v-for="(on, c) in grid[r]" :key="c" role="cell"><i class="switch" :class="{ on }"></i></span></div>
    </div>
    <div class="access-log"><small>{{ L.log }}</small>
      <TransitionGroup tag="ul" name="log"><li v-for="call in calls.slice(0, shown)" :key="call.tool + call.server" :class="call.ok ? 'ok' : 'deny'"><b>{{ call.ok ? '✓' : '✕' }}</b><code>{{ call.tool }}</code><span>→ {{ call.server }}</span><em>{{ call.ok ? L.allow : L.deny }}</em></li></TransitionGroup>
    </div>
  </figure>
</template>
