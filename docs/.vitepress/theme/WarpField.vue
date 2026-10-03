<script setup lang="ts">
// A field of terminal glyphs that bends around the pointer like light around a mass.
// Without a pointer (touch, idle) the lens drifts on its own; reduced motion draws one still frame.
import { onMounted, onUnmounted, ref } from 'vue'
const props = withDefaults(defineProps<{ intensity?: number; cell?: number }>(), { intensity: 1, cell: 14 })
const canvas = ref<HTMLCanvasElement>()
const RAMP = ' ·.:-=+*≡#'
const GLYPHS = '$>_/{}[]|01~#@%&ssh'
let raf = 0, visible = true, stop = () => {}

onMounted(async () => {
  const el = canvas.value!, ctx = el.getContext('2d')!
  const still = matchMedia('(prefers-reduced-motion: reduce)').matches
  let w = 0, h = 0, cols = 0, rows = 0, cw = props.cell * 0.62, ch = props.cell * 1.45
  const pointer = { x: -1, y: -1, tx: -1, ty: -1, active: false, last: 0 }
  function resize() {
    const r = el.getBoundingClientRect(), dpr = Math.min(devicePixelRatio || 1, 2)
    w = r.width; h = r.height; el.width = w * dpr; el.height = h * dpr
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.font = `${props.cell}px 'Geist Mono Variable', ui-monospace, monospace`
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
    cols = Math.ceil(w / cw) + 1; rows = Math.ceil(h / ch) + 1
  }
  function frame(t: number) {
    const s = t / 1000
    if (!pointer.active || t - pointer.last > 2600) {
      pointer.tx = w * (0.5 + 0.32 * Math.sin(s * 0.23)); pointer.ty = h * (0.45 + 0.28 * Math.sin(s * 0.31 + 1.4))
    }
    pointer.x = pointer.x < 0 ? pointer.tx : pointer.x + (pointer.tx - pointer.x) * 0.08
    pointer.y = pointer.y < 0 ? pointer.ty : pointer.y + (pointer.ty - pointer.y) * 0.08
    ctx.clearRect(0, 0, w, h)
    const R = Math.min(Math.max(w, h) * 0.22, 300), k = props.intensity
    for (let j = 0; j < rows; j++) {
      for (let i = 0; i < cols; i++) {
        let x = i * cw, y = j * ch
        const v = Math.sin(i * 0.11 + s * 0.6) + Math.sin(j * 0.17 - s * 0.45) + Math.sin((i + j) * 0.06 + s * 0.3)
        const dx = x - pointer.x, dy = y - pointer.y, d = Math.hypot(dx, dy), near = Math.max(0, 1 - d / R)
        // Lensing: cells near the mass are pushed outward along the radius and lit up.
        const push = near * near * 26 * k
        if (d > 0.001) { x += (dx / d) * push; y += (dy / d) * push }
        let level = (v + 3) / 6 * 0.55 + near * 0.6
        if (level < 0.18) continue
        level = Math.min(level, 0.999)
        const glyph = near > 0.55 ? GLYPHS[(i * 7 + j * 13 + Math.floor(s * 6)) % GLYPHS.length] : RAMP[Math.floor(level * RAMP.length)]
        const a = (0.07 + level * 0.22 + near * 0.75) * k
        const hue = 192 + near * 78 + Math.sin(s + i * 0.05) * 6
        ctx.fillStyle = near > 0.02 ? `hsla(${hue},95%,${62 + near * 18}%,${a})` : `hsla(222,40%,72%,${a * 0.75})`
        ctx.fillText(glyph, x, y)
      }
    }
    if (!still && visible) raf = requestAnimationFrame(frame)
  }
  function move(e: PointerEvent) {
    const r = el.getBoundingClientRect()
    if (e.clientY < r.top || e.clientY > r.bottom) return
    pointer.tx = e.clientX - r.left; pointer.ty = e.clientY - r.top; pointer.active = true; pointer.last = performance.now()
  }
  await document.fonts?.ready
  resize()
  const ro = new ResizeObserver(() => { resize(); if (still) frame(4000) })
  ro.observe(el)
  const io = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting
    cancelAnimationFrame(raf)
    if (visible && !still) raf = requestAnimationFrame(frame)
  })
  io.observe(el)
  addEventListener('pointermove', move, { passive: true })
  if (still) frame(4000)
  stop = () => { cancelAnimationFrame(raf); ro.disconnect(); io.disconnect(); removeEventListener('pointermove', move) }
})
onUnmounted(() => stop())
</script>

<template>
  <canvas ref="canvas" class="warp-field" aria-hidden="true"></canvas>
</template>
