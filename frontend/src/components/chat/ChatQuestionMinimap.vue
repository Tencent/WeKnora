<template>
  <nav
    v-if="visible"
    ref="rootRef"
    class="question-minimap"
    :style="{ height: `${trackHeight}px` }"
    :aria-label="t('chat.questionMinimapAriaLabel')"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
    @focusout="handleFocusOut"
  >
    <button
      class="question-minimap__rail"
      type="button"
      tabindex="0"
      :aria-label="t('chat.questionMinimapAriaLabel')"
      aria-haspopup="dialog"
      :aria-expanded="panelOpen"
      @click="handleRailClick"
      @keydown="handleRailKeydown"
      @pointermove="handleRailPointerMove"
    >
      <span
        v-for="tick in ticks"
        :key="tick.id"
        class="question-minimap__tick"
        :class="{
          'question-minimap__tick--active': visibleIds.has(tick.id),
          'question-minimap__tick--previewed': panelOpen && peakId === tick.id,
        }"
        :style="{
          top: `${tick.yPx}px`,
          transform: `translateY(-50%) scaleX(${tickDisplayScale(tick.yPx, mountainPointerY, visibleIds.has(tick.id))})`,
        }"
      />
    </button>

    <div
      v-if="panelOpen"
      class="question-minimap__bridge"
      aria-hidden="true"
    />

    <section
      v-if="panelOpen && peakTurn"
      class="question-minimap__panel"
      role="dialog"
      :aria-label="t('chat.questionMinimapTitle')"
      :style="{ top: `${peakYPx}px` }"
      @keydown.esc.stop.prevent="closePanel"
    >
      <button type="button" class="question-minimap__preview" @click="handleQuestionClick(peakTurn.id)">
        <span class="question-minimap__meta">
          <t-icon name="chat" size="13px" aria-hidden="true" />
          <span class="question-minimap__position">{{ t('chat.questionMinimapPosition', { current: peakIndex + 1, total: anchoredQuestions.length }) }}</span>
          <t-icon name="arrow-up" size="14px" class="question-minimap__jump-icon" aria-hidden="true" />
        </span>
        <span class="question-minimap__question">{{ questionText(peakTurn) }}</span>
        <span v-if="answerText(peakTurn)" class="question-minimap__answer">{{ answerText(peakTurn) }}</span>
      </button>
    </section>
  </nav>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChatQuestionMinimap } from '@/composables/useChatQuestionMinimap'
import {
  answerPreviewText,
  nearestTickId,
  questionDisplayText,
  tickDisplayScale,
  type ChatMessageLike,
  type OutlineMessage,
} from '@/utils/chatQuestionMinimap'

const OPEN_DELAY_MS = 120
const CLOSE_DELAY_MS = 180

const props = defineProps<{
  scrollContainer: HTMLElement | null
  messages: ChatMessageLike[]
}>()

const emit = defineEmits<{
  (event: 'jump', messageId: string): void
}>()

const { t } = useI18n()
const rootRef = ref<HTMLElement | null>(null)
const hoverOpen = ref(false)
const pinnedOpen = ref(false)
const hoveredId = ref<string | null>(null)
const pointerY = ref<number | null>(null)
const keyboardIndex = ref(-1)
const isCoarsePointer = ref(false)

const {
  visible,
  questions,
  ticks,
  activeId,
  visibleIds,
  anchoredIds,
  trackHeight,
} = useChatQuestionMinimap({
  scrollContainer: toRef(props, 'scrollContainer'),
  messages: toRef(props, 'messages'),
})

const panelOpen = computed(() => hoverOpen.value || pinnedOpen.value)
const anchoredQuestions = computed(() => (
  questions.value.filter((question) => anchoredIds.value.has(question.id))
))
const peakId = computed(() => {
  if (keyboardIndex.value >= 0) {
    return questions.value[keyboardIndex.value]?.id ?? null
  }
  return hoveredId.value ?? activeId.value
})
const peakTurn = computed(() => (
  questions.value.find((question) => question.id === peakId.value) ?? null
))
const peakIndex = computed(() => anchoredQuestions.value.findIndex(turn => turn.id === peakId.value))
const peakYPx = computed(() => {
  const tick = ticks.value.find((item) => item.id === peakId.value)
  return tick?.yPx ?? 0
})
const mountainPointerY = computed(() => {
  if (keyboardIndex.value >= 0) return peakYPx.value
  return pointerY.value
})

let closeTimer: number | null = null
let openTimer: number | null = null

const questionText = (question: OutlineMessage) => (
  question.role === 'assistant'
    ? answerPreviewText(question.content) || t('chat.thinking')
    : questionDisplayText(question.content, t('chat.questionMinimapAttachmentPlaceholder'))
)

const answerText = (question: OutlineMessage) => (
  answerPreviewText(question.answerContent)
)

const clearCloseTimer = () => {
  if (closeTimer === null) return

  window.clearTimeout(closeTimer)
  closeTimer = null
}

const clearOpenTimer = () => {
  if (openTimer !== null) window.clearTimeout(openTimer)
  openTimer = null
}

const openPanel = () => {
  clearOpenTimer()
  clearCloseTimer()
  hoverOpen.value = true
  keyboardIndex.value = -1
}

const closePanel = () => {
  clearOpenTimer()
  clearCloseTimer()
  hoverOpen.value = false
  pinnedOpen.value = false
  hoveredId.value = null
  pointerY.value = null
  keyboardIndex.value = -1
}

watch(visible, (isVisible) => {
  if (!isVisible) closePanel()
})

const scheduleClose = () => {
  clearOpenTimer()
  clearCloseTimer()
  closeTimer = window.setTimeout(() => {
    hoverOpen.value = false
    hoveredId.value = null
    pointerY.value = null
    closeTimer = null
  }, CLOSE_DELAY_MS)
}

const syncKeyboardIndexToActive = () => {
  const index = questions.value.findIndex((question) => question.id === activeId.value)
  keyboardIndex.value = index >= 0 && anchoredIds.value.has(questions.value[index].id) ? index : -1
}

const handleMouseEnter = () => {
  if (isCoarsePointer.value) return
  clearCloseTimer()
  if (panelOpen.value || openTimer !== null) return
  openTimer = window.setTimeout(openPanel, OPEN_DELAY_MS)
}

const handleMouseLeave = () => {
  if (isCoarsePointer.value) return
  scheduleClose()
}

const handleRailPointerMove = (event: PointerEvent) => {
  if (isCoarsePointer.value) return

  const target = event.currentTarget as HTMLElement
  const rect = target.getBoundingClientRect()
  pointerY.value = (event.clientY - rect.top) * trackHeight.value / (rect.height || 1)
  hoveredId.value = nearestTickId(ticks.value, pointerY.value)
  keyboardIndex.value = -1
}

const handleRailClick = (event: MouseEvent) => {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const id = event.detail === 0 ? peakId.value : nearestTickId(
    ticks.value, (event.clientY - rect.top) * trackHeight.value / (rect.height || 1),
  )
  if (!isCoarsePointer.value) {
    if (id) handleQuestionClick(id)
    return
  }

  clearOpenTimer()
  clearCloseTimer()
  pinnedOpen.value = !pinnedOpen.value || hoveredId.value !== id
  hoverOpen.value = false
  hoveredId.value = id
}

const handleFocusOut = (event: FocusEvent) => {
  if (!rootRef.value?.contains(event.relatedTarget as Node | null)) closePanel()
}

const handleQuestionClick = (id: string) => {
  if (!anchoredIds.value.has(id)) return

  closePanel()
  emit('jump', id)
}

const moveKeyboard = (direction: 1 | -1) => {
  const anchored = anchoredQuestions.value
  if (anchored.length === 0) return

  const wasOpen = panelOpen.value
  clearOpenTimer()
  clearCloseTimer()
  pointerY.value = null
  hoveredId.value = null
  hoverOpen.value = true
  if (!wasOpen) {
    syncKeyboardIndexToActive()
  }
  const currentQuestion = questions.value[keyboardIndex.value]
  const currentAnchoredIndex = currentQuestion
    ? anchored.findIndex((question) => question.id === currentQuestion.id)
    : -1
  const activeAnchoredIndex = anchored.findIndex((question) => question.id === activeId.value)
  const startIndex = currentAnchoredIndex >= 0 ? currentAnchoredIndex : activeAnchoredIndex
  const nextAnchoredIndex = Math.min(
    anchored.length - 1,
    Math.max(0, (startIndex >= 0 ? startIndex : 0) + direction),
  )
  const nextQuestion = anchored[nextAnchoredIndex]
  keyboardIndex.value = questions.value.findIndex((question) => question.id === nextQuestion.id)
}

const jumpKeyboardQuestion = () => {
  const question = peakTurn.value
  if (!question || !anchoredIds.value.has(question.id)) return

  closePanel()
  emit('jump', question.id)
}

const handleRailKeydown = (event: KeyboardEvent) => {
  if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
    event.preventDefault()
    moveKeyboard(1)
    return
  }

  if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
    event.preventDefault()
    moveKeyboard(-1)
    return
  }

  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    jumpKeyboardQuestion()
    return
  }

  if (event.key === 'Home' || event.key === 'End') {
    event.preventDefault()
    openPanel()
    const target = event.key === 'Home' ? anchoredQuestions.value[0] : anchoredQuestions.value.at(-1)
    keyboardIndex.value = questions.value.findIndex(question => question.id === target?.id)
    return
  }

  if (event.key === 'Escape') {
    event.preventDefault()
    closePanel()
  }
}

const handleDocumentPointerDown = (event: PointerEvent) => {
  if (!panelOpen.value && openTimer === null) return
  const target = event.target as Node | null
  if (target && rootRef.value?.contains(target)) return

  closePanel()
}

onMounted(() => {
  isCoarsePointer.value = window.matchMedia('(pointer: coarse)').matches
  document.addEventListener('pointerdown', handleDocumentPointerDown)
})

onBeforeUnmount(() => {
  clearOpenTimer()
  clearCloseTimer()
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
})
</script>

<style scoped lang="less">
.question-minimap {
  position: absolute;
  top: 50%;
  left: var(--chat-content-inset, 20px);
  z-index: 11;
  display: flex;
  align-items: stretch;
  transform: translateY(-50%);
  pointer-events: none;
  font-size: var(--app-text-md);
  line-height: 1.45;
}

.question-minimap__rail,
.question-minimap__bridge,
.question-minimap__panel {
  pointer-events: auto;
}

.question-minimap__rail {
  position: relative;
  width: 28px;
  height: 100%;
  padding: 0;
  overflow: visible;
  border: 0;
  background: transparent;
  cursor: pointer;
}

.question-minimap__rail::before {
  content: '';
  position: absolute;
  inset: -8px -4px;
  border-radius: 6px;
}

.question-minimap__rail:focus-visible {
  outline: none;
}

.question-minimap__rail:focus-visible::before {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 2px;
}

.question-minimap__tick {
  position: absolute;
  left: 0;
  width: 9px;
  height: 2px;
  border-radius: 1px;
  background: var(--td-text-color-secondary);
  opacity: 0.4;
  transform: translateY(-50%);
  transform-origin: left center;
  transition: transform var(--app-motion-instant) ease-out, background var(--app-motion-instant) ease-out, opacity var(--app-motion-instant) ease-out;
}

.question-minimap__tick--previewed {
  background: var(--td-text-color-primary);
  opacity: 0.7;
}

.question-minimap__tick--active {
  background: var(--td-brand-color);
  opacity: 0.85;
}

.question-minimap__bridge {
  width: 8px;
}

.question-minimap__panel {
  position: absolute;
  left: 28px;
  width: 264px;
  max-width: calc(100vw - 80px);
  max-height: min(280px, 40vh);
  padding: 0;
  overflow: hidden;
  scrollbar-width: none;
  border: 0.5px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.04), 0 6px 20px rgba(0, 0, 0, 0.08);
  transition: top var(--app-motion-fast) ease-out;
  transform: translateY(-50%);
  cursor: pointer;
}

.question-minimap__panel::-webkit-scrollbar {
  display: none;
}

.question-minimap__preview {
  display: block;
  width: 100%;
  padding: 12px 14px;
  border: 0;
  text-align: left;
  font-family: var(--app-font-family);
  background: transparent;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;

  &:hover { background: var(--td-bg-color-container-hover); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -3px; border-radius: var(--app-radius-lg); }
}

.question-minimap__meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.question-minimap__position {
  font-variant-numeric: tabular-nums;
}

.question-minimap__jump-icon { margin-left: auto; transform: rotate(-45deg); }

.question-minimap__question,
.question-minimap__answer {
  margin: 0;
  font-size: var(--app-text-md);
  font-weight: 400;
  line-height: 1.6;
}

.question-minimap__question {
  display: -webkit-box;
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-weight: 500;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow-wrap: anywhere;
}

.question-minimap__answer {
  display: -webkit-box;
  margin-top: 6px;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  overflow-wrap: anywhere;
}

@media (prefers-reduced-motion: reduce) {
  .question-minimap__tick,
  .question-minimap__panel {
    transition: none;
  }
}
</style>
