<script setup lang="ts">
import { withBase } from 'vitepress'
</script>
<template>
  <section id="architecture" class="section-wrap architecture-section">
    <div class="architecture-heading"><div><p class="eyebrow">02 / ARCHITECTURE</p><h2>一个客户端，<br>多种 Agent 框架。</h2></div><p>原生桌面客户端提供交互，本地 Adapter 连接不同 Harness。模型请求与工具执行各走自己的通道。</p></div>
    <div class="architecture-board">
      <div class="col"><i class="tag">01 · Client</i><div class="architecture-node client-node"><img :src="withBase('/openwarp-icon.png')" alt="" width="36" height="36"><strong>OpenWarp</strong><span>原生桌面客户端</span><small>终端 · Agent 对话<br>SSH 连接管理</small></div></div>
      <span class="architecture-arrow" aria-hidden="true">→</span>
      <div class="col"><i class="tag">02 · Local</i><div class="architecture-node"><small>本地协调层</small><strong>Adapter /<br>Agent Engine</strong><span>会话与配置<br>Runtime 对接 · 工具协议</span></div></div>
      <span class="architecture-arrow" aria-hidden="true">→</span>
      <div class="col"><i class="tag">03 · Harness</i><div class="harness-options"><div><b>Native</b><span>内置 Harness</span></div><div><b>Pi Agent</b><span>Pi Harness</span></div><div><b>DSH</b><span>DeepSeek Harness</span></div></div></div>
      <span class="architecture-arrow" aria-hidden="true">→</span>
      <div class="col"><i class="tag">04 · Channels</i><div class="architecture-branches"><div><small>模型请求 / 响应</small><strong>Model API</strong><span>OpenAI-compatible API<br>远程服务或本地模型</span></div><div><small>工具调用 / 结果</small><strong>Local / SSH Tools</strong><span>本地项目或远端服务器<br>命令不通过模型服务执行</span></div></div></div>
    </div>
    <div class="architecture-mcp"><strong>SSH MCP</strong><p>Codex / Claude Code <span>──▸</span> 本地 MCP bridge <span>──▸</span> OpenWarp 授权与 SSH 工具 <span>──▸</span> 服务器</p></div>
    <div class="architecture-bottom"><p>外部 Agent 使用自己的 Harness，不必经过 OpenWarp 的模型工作循环。</p><a class="text-link" :href="withBase('/guide/architecture')">架构与实现详解 ↗</a></div>
  </section>
</template>
