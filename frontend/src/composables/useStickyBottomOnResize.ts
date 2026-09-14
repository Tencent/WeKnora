import { onMounted, onUnmounted, type Ref } from 'vue'

/**
 * Keep a chat pinned to the bottom when asynchronously rendered content grows.
 *
 * Streaming text already requests scrolling when chunks arrive, but images,
 * Mermaid diagrams, and other rich content can change height later. Observe the
 * message list itself so all delayed layout changes share the same behavior.
 * A user who has intentionally scrolled up is never pulled back down.
 */
export function useStickyBottomOnResize(
  scrollContainer: Ref<HTMLElement | null>,
  userHasScrolledUp: Ref<boolean>,
): void {
  let resizeObserver: ResizeObserver | null = null

  const followResize = () => {
    if (userHasScrolledUp.value) return
    const container = scrollContainer.value
    if (!container) return

    // ResizeObserver runs after layout, before paint. Deferring to rAF here
    // paints one frame with a trailing steer bubble displaced by the growing
    // answer, then snaps it back on the next frame. Correct the scroll offset
    // now; changing scrollTop does not resize the observed message list.
    container.scrollTop = container.scrollHeight
  }

  onMounted(() => {
    const messageList = scrollContainer.value?.firstElementChild
    if (!messageList || typeof ResizeObserver === 'undefined') return

    resizeObserver = new ResizeObserver(followResize)
    resizeObserver.observe(messageList)
  })

  onUnmounted(() => {
    resizeObserver?.disconnect()
    resizeObserver = null
  })
}
