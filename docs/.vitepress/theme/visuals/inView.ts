import { onMounted, onUnmounted, ref, type Ref } from 'vue'

// Flips to true once the element scrolls into view; true immediately when motion is reduced.
export function useInView(el: Ref<HTMLElement | undefined>) {
  const seen = ref(false)
  const still = ref(false)
  let observer: IntersectionObserver | undefined
  onMounted(() => {
    still.value = matchMedia('(prefers-reduced-motion: reduce)').matches
    if (still.value || !('IntersectionObserver' in window)) { seen.value = true; return }
    observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) { seen.value = true; observer?.disconnect() }
    }, { threshold: 0.35 })
    if (el.value) observer.observe(el.value)
  })
  onUnmounted(() => observer?.disconnect())
  return { seen, still }
}

export const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))
