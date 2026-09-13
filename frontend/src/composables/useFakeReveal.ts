import { getCurrentInstance, onBeforeUnmount, ref, watch, type Ref } from 'vue'

export interface FakeRevealOptions {
  intervalMs?: number
}

/** Reveal a validated list progressively to simulate a lightweight stream. */
export function useFakeReveal<T>(source: Ref<readonly T[]> | (() => readonly T[]), options: FakeRevealOptions = {}) {
  const visibleCount = ref(0)
  const intervalMs = options.intervalMs ?? 80
  let timer: number | undefined
  let run = 0

  function stop() {
    if (timer !== undefined) {
      if (typeof window !== 'undefined') window.clearInterval(timer)
      timer = undefined
    }
  }

  function start(items: readonly T[]) {
    stop()
    const currentRun = ++run
    visibleCount.value = 0
    if (items.length === 0) return

    const reducedMotion = typeof window === 'undefined'
      || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reducedMotion) {
      visibleCount.value = items.length
      return
    }

    timer = window.setInterval(() => {
      if (currentRun !== run) return
      visibleCount.value = Math.min(visibleCount.value + 1, items.length)
      if (visibleCount.value >= items.length) stop()
    }, intervalMs)
  }

  watch(source, items => start(items), { deep: true, immediate: true })
  if (getCurrentInstance()) {
    onBeforeUnmount(() => {
      run += 1
      stop()
    })
  }

  return { visibleCount, stop }
}
