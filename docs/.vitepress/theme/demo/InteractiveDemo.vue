<script setup lang="ts">
import { computed, ref, onUnmounted, watch, nextTick } from 'vue'
import { scenarios } from './scenarios'
import './demo.css'
const scene = ref('local'), task = ref('deploy'), phase = ref('idle'), index = ref(-1), done = ref(0), paused = ref(false)
const transcript = ref<HTMLElement>()
const scenario = computed(() => scenarios[scene.value === 'codex' ? task.value : scene.value])
let timer: ReturnType<typeof setTimeout> | undefined
let continuation: (() => void) | undefined
function clear() { if (timer) clearTimeout(timer); timer = undefined; continuation = undefined }
function later(fn: () => void, delay: number) { continuation = fn; timer = setTimeout(() => { timer = undefined; continuation = undefined; fn() }, delay) }
function reset() { clear(); index.value = -1; done.value = 0; paused.value = false; phase.value = 'idle' }
function select(value: string) { reset(); scene.value = value; if (value === 'ssh') { phase.value = 'connecting'; later(() => { phase.value = 'enable' }, 900) } }
function enable() { phase.value = 'enabling'; later(() => { phase.value = 'ready' }, 1800) }
function advance() {
  done.value = index.value + 1
  if (done.value === scenario.value.steps.length) { phase.value = 'complete'; return }
  index.value++; later(advance, scenario.value.steps[index.value].duration)
}
function start() { clear(); done.value = 0; index.value = -1; paused.value = false; phase.value = 'running'; advance() }
function pause() {
  if (!paused.value) { if (timer) clearTimeout(timer); timer = undefined; paused.value = true }
  else { paused.value = false; const fn = continuation; if (fn) later(fn, 700) }
}
function finish() { clear(); paused.value = false; index.value = scenario.value.steps.length - 1; done.value = scenario.value.steps.length; phase.value = 'complete' }
watch(task, reset)
watch([index, done, phase], async () => { await nextTick(); if (transcript.value) transcript.value.scrollTop = transcript.value.scrollHeight })
onUnmounted(clear)
</script>

<template>
  <section id="interactive-demo" class="section-wrap interactive-demo" aria-label="交互产品演示">
    <div class="demo-selector" role="group" aria-label="选择演示场景"><button v-for="(label, key) in {local:'01 本地 Agent',ssh:'02 SSH Agent',codex:'03 Codex + SSH MCP'}" :key="key" :aria-pressed="scene === key" @click="select(key)">{{ label }}</button></div>
    <p class="demo-disclaimer">模拟演示</p>
    <div class="demo-window" :class="{ 'codex-demo': scene === 'codex' }">
      <header class="demo-titlebar"><span class="traffic" aria-hidden="true">● ● ●</span><span>{{ scene === 'codex' ? 'Codex · demo-api' : 'OpenWarp' }}</span><span class="demo-badge">{{ scene === 'codex' ? 'MCP · OpenWarp' : '⌕  Search sessions, agents, files…' }}</span></header>
      <div class="demo-workspace">
        <aside class="demo-sidebar"><span class="sidebar-label">{{ scene === 'codex' ? 'PROJECT' : 'WORKSPACES' }}</span><button :class="{selected:scene === 'local'}" @click="select('local')">⌘ 本地工作空间<small>~/workspace/demo-api</small></button><span class="sidebar-label">SSH</span><button :class="{selected:scene === 'ssh'}" @click="select('ssh')">⌁ demo-server<small>developer@demo-server</small></button><span class="sidebar-label">INTEGRATIONS</span><button :class="{selected:scene === 'codex'}" @click="select('codex')">↗ Codex + MCP<small>构建 · 部署 · 修复</small></button></aside>
        <div class="demo-main"><div class="demo-context"><span>{{ scene === 'local' ? '本地 · macOS / arm64' : scene === 'ssh' ? 'SSH · Linux / x86_64' : 'demo-api / main' }}</span><span class="demo-status" role="status">{{ paused ? '已暂停' : ({idle:'等待指令',connecting:'SSH 连接中…',enable:'等待启用 Warpify',enabling:'启用中…',ready:'Agent 已就绪',running:'正在运行',complete:'已完成'})[phase] }}</span></div>
          <div ref="transcript" class="demo-transcript">
            <div v-if="scene === 'ssh' && ['connecting','enable','enabling'].includes(phase)" class="warpify-card"><span class="demo-spark">✧</span><h3>{{ phase === 'connecting' ? 'Connecting to demo-server…' : 'Enable Warpify' }}</h3><p>通过 SSH 使用本地 Agent，无需在远端安装完整 Harness。</p><pre>$ ssh developer@demo-server
Welcome to demo-server · Ubuntu Linux</pre><button v-if="phase === 'enable'" @click="enable">Enable Warpify →</button><div v-if="phase === 'enabling'" class="warpify-progress"><i></i><span>准备远程终端上下文…</span></div></div>
            <div v-else-if="index < 0" class="demo-welcome"><span class="demo-spark">{{ scene === 'codex' ? '◈' : '✧' }}</span><h3>{{ scene === 'codex' ? '构建部署或修复故障' : scene === 'ssh' ? '已连接 demo-server' : '检查本机性能' }}</h3><p>{{ scene === 'codex' ? 'Codex 负责本地开发；OpenWarp MCP 提供已授权的远程工具。' : '检查 CPU、内存和磁盘用量。' }}</p></div>
            <template v-if="index >= 0"><div class="demo-user"><span>你</span><p>{{ scenario.prompt }}</p></div><p class="demo-plan">{{ scene === 'codex' ? '我会先检查与验证，再构建发布，并读取远端状态确认结果。' : '我会先识别环境，再检查资源用量，并持续采样观察负载。' }}</p><article v-for="(step, i) in scenario.steps.slice(0,index+1)" :key="i" class="demo-step"><header><span :class="{spinning:i >= done && !paused}">{{ i < done ? '✓' : '◌' }}</span><strong>{{ step.title }}</strong><small>{{ i < done ? '完成' : paused ? '暂停' : '运行中' }}</small></header><span class="tool-label">{{ step.tool }}</span><pre class="demo-command">{{ step.command }}</pre><pre v-if="i < done" class="demo-output">{{ step.output }}</pre><div v-else class="demo-pending">{{ paused ? '已暂停' : step.duration > 3000 ? '任务持续运行，正在收集采样输出…' : '等待工具结果…' }}</div></article><div v-if="phase === 'complete'" class="demo-summary"><strong>✓ {{ scene === 'codex' ? '任务完成' : '检查完成' }}</strong><p>{{ scenario.summary }}</p></div></template>
          </div>
          <div class="demo-composer"><div v-if="scene === 'codex'" class="demo-tasks"><button :disabled="phase === 'running'" :aria-pressed="task === 'deploy'" @click="task = 'deploy'">构建并部署</button><button :disabled="phase === 'running'" :aria-pressed="task === 'fix'" @click="task = 'fix'">读日志 → 修复 bug → 部署</button></div><span class="tool-label">{{ scene === 'codex' ? 'OpenWarp MCP' : 'Agent' }}</span><p>{{ scenario.prompt }}</p><div class="demo-controls"><span>{{ scene === 'ssh' ? 'SSH · demo-server · Pi Agent' : scene === 'codex' ? '本地开发 + 远程 MCP 工具' : '本地 · Native Agent' }}</span><button v-if="phase === 'running'" @click="pause">{{ paused ? '继续' : '暂停' }}</button><button v-if="index >= 0 && phase !== 'complete'" @click="finish">完整结果</button><button class="demo-send" :disabled="['connecting','enable','enabling'].includes(phase)" @click="start">{{ phase === 'running' || phase === 'complete' ? '重新播放 ↻' : '发送 ↑' }}</button></div></div>
        </div>
      </div>
    </div>
  </section>
</template>
