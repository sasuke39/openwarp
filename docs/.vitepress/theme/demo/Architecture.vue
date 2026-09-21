<script setup lang="ts">
import { withBase } from 'vitepress'
</script>
<template>
  <section class="section-wrap architecture-section">
    <p class="eyebrow">01 / HOW IT WORKS</p><h2>客户端、Adapter<br>与 Harness</h2>
    <p class="architecture-intro">原生桌面客户端提供交互，本地 Adapter 连接不同 Harness。模型请求与工具执行各走自己的通道。</p>
    <div class="architecture-board">
      <div class="architecture-node"><small>交互层</small><strong>OpenWarp 原生桌面客户端</strong><span>终端 · Agent 对话 · SSH 连接管理</span></div><div class="architecture-arrow">↓ 请求 / ↑ 流式结果</div>
      <div class="architecture-node"><small>本地协调层</small><strong>Adapter / Agent Engine</strong><span>会话与配置 · Runtime 对接 · 工具协议</span></div><div class="architecture-arrow">↓ 选择运行框架</div>
      <div class="harness-options"><span>Native</span><span>Pi Agent</span><span>DeepSeek Harness · DSH</span></div>
      <div class="architecture-branches"><div><p>↙ 模型请求 / 响应</p><strong>你配置的模型服务</strong><span>OpenAI-compatible API<br>远程服务或本地模型</span></div><div><p>↘ 工具调用 / 结果</p><strong>本地工具 / SSH 执行通道</strong><span>本地项目或远端服务器<br>命令不会通过模型服务执行</span></div></div>
      <div class="architecture-mcp"><strong>外部 Agent 的另一条入口</strong><p>Codex / Claude Code → 本地 MCP bridge → OpenWarp 授权与 SSH 工具 → 服务器</p><small>外部 Agent 使用自己的 Harness，不必经过 OpenWarp 的模型工作循环。</small></div>
    </div>
    <a class="text-link" :href="withBase('/guide/architecture')">架构与实现详解 →</a>
    <div class="architecture-notes"><article><h3>一套本地 Agent，多台 SSH 服务器</h3><p>无需逐台安装完整编码 Agent、Harness 或模型密钥。远端仍需要 SSH 服务、账号权限与所用命令的依赖；运行过程可能创建临时执行文件。</p></article><article><h3>运行条件</h3><p>执行任务时，本机 OpenWarp 需保持运行，SSH 连接需保持可用。需要脱离本机持续运行的任务，可选择远端部署 Agent。</p></article></div>
  </section>
</template>
<style scoped>
.architecture-section{padding:70px 0}.architecture-intro{max-width:690px;color:var(--muted);line-height:1.9;margin:24px 0}.architecture-board{border:1px solid var(--line);border-radius:10px;padding:32px;margin:30px 0;background:var(--surface-subtle)}.architecture-node{background:var(--surface);border:1px solid var(--line);padding:20px;text-align:center;display:grid;gap:6px}.architecture-node small,.architecture-node span{color:var(--muted);font-size:12px}.architecture-arrow{text-align:center;font:11px/3 var(--ow-mono);color:var(--accent)}.harness-options{display:flex;gap:12px}.harness-options span{flex:1;text-align:center;border:1px solid var(--accent);padding:15px;font-size:13px;background:var(--surface)}.architecture-branches{display:grid;grid-template-columns:1fr 1fr;gap:20px;margin-top:20px}.architecture-branches>div{display:grid;gap:10px;padding:18px;border:1px solid var(--line)}.architecture-branches p{color:var(--accent);font-size:12px}.architecture-branches span{font-size:13px;color:var(--muted);line-height:1.8}.architecture-mcp{border-top:1px solid var(--line);margin-top:24px;padding-top:24px;font-size:13px;line-height:2}.architecture-mcp small{color:var(--muted)}.architecture-notes{display:grid;grid-template-columns:1fr 1fr;gap:40px;margin-top:32px}.architecture-notes p{color:var(--muted);font-size:14px;line-height:1.9;margin-top:12px}@media(max-width:640px){.architecture-board{padding:16px}.harness-options{flex-direction:column}.architecture-branches,.architecture-notes{grid-template-columns:1fr}}
</style>
