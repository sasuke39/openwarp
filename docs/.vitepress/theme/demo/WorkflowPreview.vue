<script setup lang="ts">
import { scenarios } from './scenarios'
defineProps<{ kind: 'ssh' | 'mcp' }>()
</script>

<template>
  <figure class="workflow-preview" :aria-label="kind === 'ssh' ? 'SSH Agent 执行示例' : 'Codex MCP 部署示例'">
    <header><span class="window-dots" aria-hidden="true"><i></i><i></i><i></i></span><span>{{ kind === 'ssh' ? 'OpenWarp — demo-server' : 'Codex — demo-api' }}</span><small>{{ kind === 'ssh' ? 'SSH' : 'MCP · OpenWarp' }}</small></header>
    <div v-if="kind === 'ssh'" class="workflow-output">
      <div class="warpify-ready"><span>✓</span><div><strong>Enable Warpify</strong><p>本地 Agent 已接入远程终端</p></div><small>READY</small></div>
      <template v-for="step in scenarios.ssh.steps" :key="step.title"><p class="terminal-prompt">developer@demo-server <span>~</span></p><pre><b>$</b> {{ step.command }}</pre><pre class="terminal-result">{{ step.output }}</pre></template>
      <div class="terminal-cursor"><span>❯</span><i></i></div>
    </div>
    <ol v-else class="workflow-timeline">
      <li v-for="(step, i) in [scenarios.fix.steps[0], scenarios.fix.steps[2], scenarios.fix.steps[4], scenarios.fix.steps[6]]" :key="step.title"><span>{{ i + 1 }}</span><div><strong>{{ step.title }}</strong><small>{{ step.tool }}</small><pre>{{ step.command.split('\n')[0] }}</pre><p :class="{ 'log-error': i === 0 }">{{ ['ERROR POST /orders 500 · req.Customer.ID', 'PASS TestCreateOrderWithoutCustomer', 'Upload complete · demo-server', 'active · HTTP 200 · 无新增 panic'][i] }}</p></div></li>
    </ol>
    <figcaption>{{ kind === 'ssh' ? '本地 Agent → SSH 通道 → 远程工具' : '读取日志 → 本地修复 → 构建部署 → 回归验证' }}</figcaption>
  </figure>
</template>
