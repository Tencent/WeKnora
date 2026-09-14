import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  toValue,
  watch,
  type ComputedRef,
  type Ref,
} from 'vue'
import {
  activeQuestionId,
  collectOutlineMessages,
  isChatOverflowing,
  mapQuestionTicks,
  offsetFromScrollContent,
  questionMinimapTrackHeight,
  shouldShowQuestionMinimap,
  viewportBand,
  visibleMessageIds,
  type ChatMessageLike,
  type QuestionTick,
  type OutlineMessage,
  type ViewportBand,
} from '@/utils/chatQuestionMinimap'

export function useChatQuestionMinimap(options: {
  scrollContainer: Ref<HTMLElement | null>
  messages: ChatMessageLike[] | Ref<ChatMessageLike[]>
}): {
  visible: ComputedRef<boolean>
  questions: ComputedRef<OutlineMessage[]>
  ticks: Ref<QuestionTick[]>
  viewport: Ref<ViewportBand>
  activeId: Ref<string | null>
  visibleIds: Ref<Set<string>>
  anchoredIds: Ref<Set<string>>
  scrollbarGutterPx: Ref<number>
  trackHeight: Ref<number>
} {
  const questions = computed(() => collectOutlineMessages(toValue(options.messages)))
  const overflowing = ref(false)
  const ticks = ref<QuestionTick[]>([])
  const viewport = ref<ViewportBand>({ topPx: 0, heightPx: 0 })
  const activeId = ref<string | null>(null)
  const visibleIds = ref<Set<string>>(new Set())
  const anchoredIds = ref<Set<string>>(new Set())
  const scrollbarGutterPx = ref(0)
  const trackHeight = ref(0)

  const visible = computed(() => (
    shouldShowQuestionMinimap(overflowing.value, questions.value.length)
  ))

  let resizeObserver: ResizeObserver | null = null
  let observedContainer: HTMLElement | null = null
  let observedContent: Element | null = null
  let frameId: number | null = null

  const reset = () => {
    overflowing.value = false
    ticks.value = []
    viewport.value = { topPx: 0, heightPx: 0 }
    activeId.value = null
    visibleIds.value = new Set()
    anchoredIds.value = new Set()
    scrollbarGutterPx.value = 0
    trackHeight.value = 0
  }

  const disconnectObserver = () => {
    resizeObserver?.disconnect()
    resizeObserver = null
    observedContainer = null
    observedContent = null
  }

  const measureNow = () => {
    const el = options.scrollContainer.value
    if (!el) {
      reset()
      return
    }

    overflowing.value = isChatOverflowing(el.scrollHeight, el.clientHeight)
    const anchors = Array.from(el.querySelectorAll<HTMLElement>('[data-message-id]'))
    const anchorByMessageId = new Map<string, HTMLElement>()
    for (const anchor of anchors) {
      const messageId = anchor.dataset.messageId
      if (messageId) anchorByMessageId.set(messageId, anchor)
    }
    const containerTop = el.getBoundingClientRect().top
    scrollbarGutterPx.value = Math.max(0, el.offsetWidth - el.clientWidth)
    const measured = questions.value.flatMap((question) => {
      const anchor = anchorByMessageId.get(question.id)
      if (!anchor) return []
      const rect = anchor.getBoundingClientRect()
      if (rect.height <= 0) return []

      return [{
        id: question.id,
        offsetTop: offsetFromScrollContent(
          rect.top,
          containerTop,
          el.scrollTop,
        ),
        offsetBottom: offsetFromScrollContent(rect.bottom, containerTop, el.scrollTop),
      }]
    })

    trackHeight.value = questionMinimapTrackHeight(measured.length, el.clientHeight)
    ticks.value = mapQuestionTicks(measured, trackHeight.value)
    viewport.value = viewportBand(el.scrollTop, el.clientHeight, el.scrollHeight, trackHeight.value)
    activeId.value = activeQuestionId(measured, el.scrollTop)
    visibleIds.value = visibleMessageIds(measured, el.scrollTop, el.clientHeight)
    anchoredIds.value = new Set(measured.map((item) => item.id))
  }

  const scheduleMeasure = () => {
    if (frameId !== null) return

    frameId = window.requestAnimationFrame(() => {
      frameId = null
      measureNow()
    })
  }

  const observeCurrentContainer = () => {
    const el = options.scrollContainer.value
    if (!el || typeof ResizeObserver === 'undefined') return

    const content = el.firstElementChild
    if (resizeObserver && observedContainer === el && observedContent === content) return

    disconnectObserver()
    resizeObserver = new ResizeObserver(scheduleMeasure)
    resizeObserver.observe(el)
    if (content) resizeObserver.observe(content)
    observedContainer = el
    observedContent = content
  }

  const bind = () => {
    const el = options.scrollContainer.value
    if (!el) {
      disconnectObserver()
      reset()
      return
    }

    el.addEventListener('scroll', scheduleMeasure, { passive: true })
    window.addEventListener('resize', scheduleMeasure)
    observeCurrentContainer()
    scheduleMeasure()
  }

  const unbind = (el: HTMLElement | null) => {
    el?.removeEventListener('scroll', scheduleMeasure)
    window.removeEventListener('resize', scheduleMeasure)
    disconnectObserver()
  }

  watch(options.scrollContainer, (el, previousEl) => {
    unbind(previousEl)
    if (el) bind()
    else reset()
  }, { flush: 'post' })

  watch(questions, () => {
    observeCurrentContainer()
    scheduleMeasure()
  }, { flush: 'post' })

  onMounted(bind)

  onBeforeUnmount(() => {
    unbind(options.scrollContainer.value)
    if (frameId !== null) {
      window.cancelAnimationFrame(frameId)
      frameId = null
    }
  })

  return {
    visible,
    questions,
    ticks,
    viewport,
    activeId,
    visibleIds,
    anchoredIds,
    scrollbarGutterPx,
    trackHeight,
  }
}
