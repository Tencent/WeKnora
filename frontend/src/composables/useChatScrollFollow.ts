import { computed, nextTick, onMounted, onUnmounted, provide, ref, watch, type InjectionKey, type Ref } from 'vue'
import { useStickyBottomOnResize } from './useStickyBottomOnResize'

export const chatReadingHistoryKey: InjectionKey<Readonly<Ref<boolean>>> = Symbol('chat-reading-history')

/** One follow policy for the parent conversation and each child viewport. */
export function useChatScrollFollow(
  viewport: Ref<HTMLElement | null>,
  reading: Ref<boolean>,
  active: Readonly<Ref<boolean>> = computed(() => true),
) {
  let lastTop = 0
  let lastHeight = 0
  let lastClientHeight = 0
  let gesture = false
  let touchY = 0
  let cleanup = () => {}
  const rememberPosition = () => {
    const el = viewport.value
    if (!el) return
    lastTop = el.scrollTop
    lastHeight = el.scrollHeight
    lastClientHeight = el.clientHeight
  }
  const atBottom = (el: HTMLElement) => el.scrollHeight - el.clientHeight - el.scrollTop <= 2
  const onScroll = () => {
    const el = viewport.value
    if (!el || !active.value) return
    const layoutChanged = el.scrollHeight !== lastHeight || el.clientHeight !== lastClientHeight
    // Resize, browser scroll anchoring and collapsing steps are not user input.
    // In particular, clamping to the bottom after a collapse must not reattach.
    if (gesture || !layoutChanged) {
      if (el.scrollTop < lastTop - 1) reading.value = true
      else if (el.scrollTop > lastTop + 1 && atBottom(el)) reading.value = false
    }
    gesture = false
    rememberPosition()
  }
  const scrollToBottom = (_force = false) => {
    // Stream callers historically pass force when a run starts/resumes. That
    // is not permission to override someone who is reading older messages.
    void nextTick(() => {
      if (!active.value || reading.value || !viewport.value) return
      viewport.value.scrollTop = viewport.value.scrollHeight
      rememberPosition()
    })
  }
  const resume = () => { reading.value = false; scrollToBottom() }
  const intent = (up: boolean) => {
    if (!active.value) return
    gesture = true
    if (up) reading.value = true
    else if (viewport.value && atBottom(viewport.value)) resume()
  }
  onMounted(() => {
    const el = viewport.value
    if (!el) return
    rememberPosition()
    const wheel = (e: WheelEvent) => { if (e.deltaY) intent(e.deltaY < 0) }
    const touchStart = (e: TouchEvent) => { touchY = e.touches[0]?.clientY || 0 }
    const touchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY ?? touchY
      if (y !== touchY) intent(y > touchY)
      touchY = y
    }
    const key = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest?.('input, textarea, [contenteditable="true"]')) return
      if (['ArrowUp', 'PageUp', 'Home'].includes(e.key) || (e.key === ' ' && e.shiftKey)) intent(true)
      else if (['ArrowDown', 'PageDown', 'End', ' '].includes(e.key)) intent(false)
    }
    const pointer = () => { gesture = true }
    el.addEventListener('wheel', wheel, { passive: true })
    el.addEventListener('touchstart', touchStart, { passive: true })
    el.addEventListener('touchmove', touchMove, { passive: true })
    el.addEventListener('keydown', key)
    el.addEventListener('pointerdown', pointer)
    cleanup = () => {
      el.removeEventListener('wheel', wheel)
      el.removeEventListener('touchstart', touchStart)
      el.removeEventListener('touchmove', touchMove)
      el.removeEventListener('keydown', key)
      el.removeEventListener('pointerdown', pointer)
    }
  })
  onUnmounted(() => cleanup())
  provide(chatReadingHistoryKey, computed(() => reading.value || !active.value))
  useStickyBottomOnResize(viewport, computed(() => reading.value || !active.value), rememberPosition)
  return { onScroll, scrollToBottom, resume, rememberPosition }
}

/** Once folded, history stays folded when the user scrolls up later. */
export function useDeferredTimelineCompletion(done: Readonly<Ref<boolean>>, reading: Readonly<Ref<boolean>>) {
  const settled = ref(done.value)
  watch([done, reading], ([complete, paused]) => {
    if (!complete) settled.value = false
    else if (!paused) settled.value = true
  }, { flush: 'sync' })
  return settled
}
