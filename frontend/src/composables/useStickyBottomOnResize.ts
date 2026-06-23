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
  scrollToBottom: () => void,
): void {
  let resizeObserver: ResizeObserver | null = null
  let scrollFrame: number | null = null

  const scheduleFollow = () => {
    if (userHasScrolledUp.value || scrollFrame !== null) return

    scrollFrame = requestAnimationFrame(() => {
      scrollFrame = null
      if (!userHasScrolledUp.value) scrollToBottom()
    })
  }

  onMounted(() => {
    const messageList = scrollContainer.value?.firstElementChild
    if (!messageList || typeof ResizeObserver === 'undefined') return

    resizeObserver = new ResizeObserver(scheduleFollow)
    resizeObserver.observe(messageList)
  })

  onUnmounted(() => {
    resizeObserver?.disconnect()
    resizeObserver = null
    if (scrollFrame !== null) {
      cancelAnimationFrame(scrollFrame)
      scrollFrame = null
    }
  })
}
