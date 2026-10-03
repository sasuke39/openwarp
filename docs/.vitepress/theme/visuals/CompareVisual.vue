<script setup lang="ts">
import { ref } from 'vue'
import { useInView } from './inView'
const props = defineProps<{ zh: boolean }>()
const root = ref<HTMLElement>()
const { seen } = useInView(root)
const servers = ['demo-server', 'staging-db', 'prod-api']
// Row centres inside the 216px-tall stage; the SVG uses the same units so nothing stretches.
const rows = [28, 108, 188]
const stack = ['Agent', 'Harness', 'API Key']
const L = props.zh
  ? { before: '以前', beforeSub: '每台服务器各装一套', after: '用 OpenWarp', afterSub: '只在你的 Mac 上装一套', mac: '你的 Mac', ssh: 'SSH', beforeSum: ['3 套 Agent', '3 份密钥', '逐台改配置'], afterSum: ['1 套 Agent', '1 份密钥', '服务器零安装'] }
  : { before: 'Before', beforeSub: 'A full stack on every server', after: 'With OpenWarp', afterSub: 'One stack, on your Mac', mac: 'Your Mac', ssh: 'SSH', beforeSum: ['3 Agents', '3 API keys', 'edit every host'], afterSum: ['1 Agent', '1 API key', 'nothing on servers'] }
</script>

<template>
  <div ref="root" class="cmp" :class="{ play: seen }">
    <div class="cmp-side before">
      <header><strong>{{ L.before }}</strong><span>{{ L.beforeSub }}</span></header>
      <div class="cmp-stage">
        <div v-for="(s, i) in servers" :key="s" class="cmp-node server" :style="{ '--d': i }">
          <span class="host"><i class="dot"></i>{{ s }}</span>
          <span class="stack"><em v-for="item in stack" :key="item">{{ item }}</em></span>
        </div>
      </div>
      <footer><span v-for="item in L.beforeSum" :key="item">{{ item }}</span></footer>
    </div>
    <div class="cmp-side after">
      <header><strong>{{ L.after }}</strong><span>{{ L.afterSub }}</span></header>
      <div class="cmp-stage hub">
        <div class="cmp-node mac">
          <span class="host"><i class="dot on"></i>{{ L.mac }}</span>
          <span class="stack"><em v-for="item in stack" :key="item">{{ item }}</em></span>
        </div>
        <svg class="cmp-wires" width="88" height="216" viewBox="0 0 88 216" aria-hidden="true">
          <g v-for="(y, i) in rows" :key="i">
            <path :id="`wire-${i}`" :d="`M0 108 C 44 108, 44 ${y}, 88 ${y}`" />
            <circle r="2.5"><animateMotion dur="1.8s" repeatCount="indefinite" :begin="`${i * 0.6}s`"><mpath :href="`#wire-${i}`" /></animateMotion></circle>
          </g>
        </svg>
        <div class="remotes">
          <div v-for="(s, i) in servers" :key="s" class="cmp-node remote" :style="{ '--d': i }"><span class="host"><i class="dot"></i>{{ s }}</span><span class="badge">{{ L.ssh }}</span></div>
        </div>
      </div>
      <footer><span v-for="item in L.afterSum" :key="item">{{ item }}</span></footer>
    </div>
  </div>
</template>
