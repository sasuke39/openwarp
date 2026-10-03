<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { scenarios } from './scenarios'
import './demo.css'
const scene = ref('local'), task = ref('deploy'), phase = ref('complete'), index = ref(2), done = ref(3), paused = ref(false)
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
onMounted(() => { if (transcript.value) transcript.value.scrollTop = transcript.value.scrollHeight })
const scenes = {
  local: { label: '本地 Agent', desc: '用一句话让 Agent 排查本机问题，命令在你的电脑上执行。', agent: '本机 · Native', exec: '本机 Shell', target: '~/workspace/demo-api' },
  ssh: { label: 'SSH Agent', desc: 'Agent 仍跑在本机，命令通过 SSH 在服务器上执行。服务器不用装 Agent，也不用配模型密钥。', agent: '本机 · Pi Agent', exec: 'demo-server（SSH）', target: 'developer@demo-server' },
  codex: { label: 'Codex + SSH MCP', desc: '在 Codex 里改代码，再通过 OpenWarp MCP 调用你授权过的 SSH 工具完成部署和验证。', agent: 'Codex（外部）', exec: '本机 + demo-server（MCP）', target: 'demo-api / main' }
} as const
const meta = computed(() => scenes[scene.value as keyof typeof scenes])
const statusText = computed(() => paused.value ? '已暂停' : ({ idle: '等待指令', connecting: 'SSH 连接中…', enable: '等待启用 Warpify', enabling: '启用中…', ready: 'Agent 已就绪', running: '运行中', complete: '已完成' } as Record<string, string>)[phase.value])
const secs = (ms: number) => (ms / 1000).toFixed(1) + 's'
</script>

<template>
  <section id="interactive-demo" class="interactive-demo" aria-label="交互产品演示">
    <div class="demo-rail">
      <div class="demo-intro"><p class="demo-rail-label">产品演示 <span>模拟数据</span></p><h3>一个 Agent，三种用法</h3><p>选一个场景，点「发送」看完整执行过程：Agent 怎么规划、调用了哪些命令、在哪台机器上执行、最后给出什么结论。</p></div>
      <div class="demo-selector" role="group" aria-label="选择演示场景">
        <button v-for="(item, key, i) in scenes" :key="key" :aria-pressed="scene === key" @click="select(key)"><span class="scene-index">0{{ i + 1 }}</span><strong>{{ item.label }}</strong><span class="scene-desc">{{ item.desc }}</span></button>
      </div>
      <dl class="demo-facts">
        <div><dt>Agent 运行在</dt><dd>{{ meta.agent }}</dd></div>
        <div><dt>命令执行在</dt><dd>{{ meta.exec }}</dd></div>
        <div><dt>模型</dt><dd>{{ scene === 'codex' ? 'Codex 自带' : 'OpenAI 兼容接口' }}</dd></div>
      </dl>
    </div>
    <div class="demo-window" :class="{ 'codex-demo': scene === 'codex' }">
      <header class="demo-titlebar"><span class="window-dots" aria-hidden="true"><i></i><i></i><i></i></span>
        <nav class="demo-tabs" aria-hidden="true"><template v-if="scene === 'codex'"><span class="active">Codex — demo-api</span></template><template v-else><span :class="{ active: scene === 'local' }">~/workspace/demo-api</span><span :class="{ active: scene === 'ssh' }">developer@demo-server</span></template></nav>
        <span class="demo-badge">{{ scene === 'codex' ? 'MCP · OpenWarp' : '⌘K' }}</span></header>
      <div class="demo-workspace">
        <aside class="demo-sidebar">
          <span class="sidebar-label">{{ scene === 'codex' ? '项目' : '工作空间' }}</span>
          <button :class="{ selected: scene === 'local' }" @click="select('local')"><i class="dot"></i>本地<small>~/workspace/demo-api</small></button>
          <span class="sidebar-label">SSH 连接</span>
          <button :class="{ selected: scene === 'ssh' }" @click="select('ssh')"><i class="dot ok"></i>demo-server<small>Ubuntu · 4 vCPU</small></button>
          <button disabled><i class="dot"></i>staging-db<small>未连接</small></button>
          <span class="sidebar-label">集成</span>
          <button :class="{ selected: scene === 'codex' }" @click="select('codex')"><i class="dot mcp"></i>Codex MCP<small>shell · sftp</small></button>
        </aside>
        <div class="demo-main">
          <div ref="transcript" class="demo-transcript">
            <div v-if="scene === 'ssh' && ['connecting','enable','enabling'].includes(phase)" class="warpify-card"><pre>$ ssh developer@demo-server
Welcome to demo-server · Ubuntu 22.04 LTS
developer@demo-server:~$</pre><div class="warpify-banner"><div><strong>{{ phase === 'connecting' ? '正在连接 demo-server…' : '在这台服务器上使用本地 Agent' }}</strong><p>启用 Warpify 后，Agent 的命令会通过当前 SSH 会话执行。远端无需安装任何东西。</p></div><button v-if="phase === 'enable'" @click="enable">启用 Warpify</button></div><div v-if="phase === 'enabling'" class="warpify-progress"><i></i><span>准备远程终端上下文…</span></div></div>
            <div v-else-if="index < 0" class="demo-welcome"><p class="prompt-line"><span>{{ meta.target }}</span> {{ scene === 'codex' ? '选择一个任务，点击发送开始' : '点击发送，开始演示' }}</p></div>
            <template v-if="index >= 0">
              <div class="demo-user"><span>你</span><p>{{ scenario.prompt }}</p></div>
              <p class="demo-plan">{{ scene === 'codex' ? '计划：本地测试和构建 → 通过 MCP 发布 → 读取远端状态确认。' : '计划：识别环境 → 检查资源 → 持续采样负载。' }}</p>
              <article v-for="(step, i) in scenario.steps.slice(0,index+1)" :key="i" class="demo-step" :class="{ running: i >= done }">
                <header><span class="step-state" :class="{ spinning: i >= done && !paused }">{{ i < done ? '✓' : '◌' }}</span><strong>{{ step.title }}</strong><span class="step-tool">{{ step.tool }}</span><small>{{ i < done ? secs(step.duration) : paused ? '暂停' : '运行中' }}</small></header>
                <pre class="demo-command"><span>$</span>{{ step.command }}</pre>
                <pre v-if="i < done" class="demo-output">{{ step.output }}</pre><div v-else class="demo-pending">{{ paused ? '已暂停' : step.duration > 3000 ? '后台任务运行中，正在收集采样输出…' : '等待工具结果…' }}</div>
              </article>
              <div v-if="phase === 'complete'" class="demo-summary"><header>{{ scene === 'codex' ? '任务完成' : '检查完成' }}<span>{{ scenario.steps.length }} 步 · {{ secs(scenario.steps.reduce((n, s) => n + s.duration, 0)) }}</span></header><dl><div v-for="[k, v] in scenario.facts" :key="k"><dt>{{ k }}</dt><dd>{{ v }}</dd></div></dl><p>{{ scenario.summary }}</p></div>
            </template>
          </div>
          <div class="demo-composer">
            <div v-if="scene === 'codex'" class="demo-tasks"><button :disabled="phase === 'running'" :aria-pressed="task === 'deploy'" @click="task = 'deploy'">构建并部署</button><button :disabled="phase === 'running'" :aria-pressed="task === 'fix'" @click="task = 'fix'">读日志 → 修复 → 部署</button></div>
            <p>{{ scenario.prompt }}</p>
            <div class="demo-controls"><span class="chip">{{ scene === 'ssh' ? 'Pi Agent' : scene === 'codex' ? 'Codex' : 'Native' }}</span><span class="chip">{{ scene === 'codex' ? 'OpenWarp MCP' : scene === 'ssh' ? 'SSH · demo-server' : '本地 Shell' }}</span><span class="spacer"></span><button v-if="phase === 'running'" class="ghost" @click="pause">{{ paused ? '继续' : '暂停' }}</button><button v-if="index >= 0 && phase !== 'complete'" class="ghost" @click="finish">跳到结果</button><button class="demo-send" :disabled="['connecting','enable','enabling'].includes(phase)" @click="start">{{ phase === 'running' || phase === 'complete' ? '重新播放' : '发送' }}<kbd>↵</kbd></button></div>
          </div>
        </div>
      </div>
      <footer class="demo-statusbar"><span><i class="dot" :class="{ ok: phase !== 'idle' }"></i>{{ statusText }}</span><span>{{ scene === 'local' ? 'macOS · arm64' : scene === 'ssh' ? 'Linux · x86_64' : 'demo-api · main' }}</span><span class="right">{{ Math.max(done, 0) }}/{{ scenario.steps.length }} 步</span></footer>
    </div>
  </section>
</template>
