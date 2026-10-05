import { computed, onMounted, onUnmounted, ref } from 'vue'

/** CSS and JS share the same viewport breakpoints; no persisted device state. */
export function useResponsive() {
  const width = ref(typeof window === 'undefined' ? 1024 : window.innerWidth)
  const update = () => { width.value = window.innerWidth }
  onMounted(() => window.addEventListener('resize', update))
  onUnmounted(() => window.removeEventListener('resize', update))
  return {
    isMobile: computed(() => width.value < 768),
    isTablet: computed(() => width.value >= 768 && width.value < 1024),
  }
}
