import { onBeforeUnmount, onMounted, onUpdated, watch, type Ref } from 'vue'

/**
 * Next minimum height for a streaming message. Growth raises the floor at once;
 * a shrink keeps up to `cap` pixels of slack so the following step can fill it.
 */
export function nextHeightFloor(floor: number, content: number, cap: number): number {
  return Math.max(content, Math.min(floor, content + cap))
}

function contentHeight(node: HTMLElement): number {
  const top = node.getBoundingClientRect().top
  let bottom = top
  for (const child of Array.from(node.children)) {
    bottom = Math.max(bottom, child.getBoundingClientRect().bottom)
  }
  return bottom - top
}

/**
 * While a reply streams, steps fold and previews disappear. A follow-the-bottom
 * viewport turns every such shrink into the whole conversation dropping, then
 * climbing again as the next step grows. Holding a height floor keeps scroll
 * motion one-directional; the slack is capped so the latest content stays in
 * view, and released before the completed timeline folds.
 */
export function useStreamingHeightFloor(
  el: Ref<HTMLElement | null>,
  active: Readonly<Ref<boolean>>,
  capRatio = 0.3,
): void {
  let floor = 0
  let observer: ResizeObserver | null = null

  const apply = () => {
    const node = el.value
    if (!node || !active.value) return
    const next = nextHeightFloor(floor, contentHeight(node), window.innerHeight * capRatio)
    if (next === floor) return
    floor = next
    node.style.minHeight = `${Math.ceil(next)}px`
  }

  const release = () => {
    floor = 0
    if (el.value) el.value.style.minHeight = ''
  }

  // Pre-flush: drop the floor before the render that folds the finished timeline.
  watch(active, on => (on ? apply() : release()))

  onMounted(() => {
    if (!el.value || typeof ResizeObserver === 'undefined') return
    observer = new ResizeObserver(apply)
    observer.observe(el.value)
    apply()
  })
  // A shrink held by the floor does not resize the element, so re-check the
  // slack cap after each render.
  onUpdated(apply)
  onBeforeUnmount(() => {
    observer?.disconnect()
    observer = null
  })
}
