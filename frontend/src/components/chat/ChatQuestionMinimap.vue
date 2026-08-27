<template>
  <nav
    v-if="visible"
    ref="rootRef"
    class="question-minimap"
    :style="{ left: `${RAIL_INSET_PX}px`, height: `${trackHeight}px` }"
    :aria-label="t('chat.questionMinimapAriaLabel')"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
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
        :class="{ 'question-minimap__tick--active': tick.id === peakId }"
        :style="{
          top: `${tick.yPx}px`,
          transform: `translateY(-50%) scaleX(${tickDisplayScale(tick.yPx, mountainPointerY, tick.id === peakId)})`,
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
      @click="handleQuestionClick(peakTurn.id)"
    >
      <p class="question-minimap__question" :title="questionText(peakTurn)">
        {{ questionText(peakTurn) }}
      </p>
      <p v-if="answerText(peakTurn)" class="question-minimap__answer">
        {{ answerText(peakTurn) }}
      </p>
    </section>
  </nav>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChatQuestionMinimap } from '@/composables/useChatQuestionMinimap'
import {
  answerPreviewText,
  nearestTickId,
  questionDisplayText,
  tickDisplayScale,
  type ChatMessageLike,
  type UserQuestion,
} from '@/utils/chatQuestionMinimap'

const CLOSE_DELAY_MS = 150
const RAIL_INSET_PX = 0

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
const peakYPx = computed(() => {
  const tick = ticks.value.find((item) => item.id === peakId.value)
  return tick?.yPx ?? 0
})
const mountainPointerY = computed(() => {
  if (keyboardIndex.value >= 0) return peakYPx.value
  return pointerY.value
})

let closeTimer: number | null = null

const questionText = (question: UserQuestion) => (
  questionDisplayText(question.content, t('chat.questionMinimapAttachmentPlaceholder'))
)

const answerText = (question: UserQuestion) => (
  answerPreviewText(question.answerContent)
)

const clearCloseTimer = () => {
  if (closeTimer === null) return

  window.clearTimeout(closeTimer)
  closeTimer = null
}

const openPanel = () => {
  clearCloseTimer()
  hoverOpen.value = true
  keyboardIndex.value = -1
}

const closePanel = () => {
  clearCloseTimer()
  hoverOpen.value = false
  pinnedOpen.value = false
  hoveredId.value = null
  pointerY.value = null
  keyboardIndex.value = -1
}

const scheduleClose = () => {
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
  openPanel()
}

const handleMouseLeave = () => {
  if (isCoarsePointer.value) return
  scheduleClose()
}

const handleRailPointerMove = (event: PointerEvent) => {
  if (isCoarsePointer.value) return

  const target = event.currentTarget as HTMLElement
  pointerY.value = event.clientY - target.getBoundingClientRect().top
  hoveredId.value = nearestTickId(ticks.value, pointerY.value)
}

const handleRailClick = () => {
  if (!isCoarsePointer.value) {
    const id = peakId.value
    if (id) handleQuestionClick(id)
    return
  }

  clearCloseTimer()
  pinnedOpen.value = !pinnedOpen.value
  hoverOpen.value = false
  if (pinnedOpen.value) {
    hoveredId.value = activeId.value ?? questions.value[0]?.id ?? null
  }
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
  clearCloseTimer()
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

  if (event.key === 'Enter') {
    event.preventDefault()
    jumpKeyboardQuestion()
    return
  }

  if (event.key === 'Escape') {
    event.preventDefault()
    closePanel()
  }
}

const handleDocumentPointerDown = (event: PointerEvent) => {
  if (!isCoarsePointer.value || !panelOpen.value) return
  const target = event.target as Node | null
  if (target && rootRef.value?.contains(target)) return

  closePanel()
}

onMounted(() => {
  isCoarsePointer.value = window.matchMedia('(pointer: coarse)').matches
  document.addEventListener('pointerdown', handleDocumentPointerDown)
})

onBeforeUnmount(() => {
  clearCloseTimer()
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
})
</script>

<style scoped lang="less">
.question-minimap {
  position: absolute;
  top: 50%;
  left: 0;
  z-index: 11;
  display: flex;
  align-items: stretch;
  transform: translateY(-50%);
  pointer-events: none;
  font-size: 13px;
  line-height: 1.45;
}

.question-minimap__rail,
.question-minimap__bridge,
.question-minimap__panel {
  pointer-events: auto;
}

.question-minimap__rail {
  position: relative;
  width: 24px;
  height: 100%;
  padding: 0;
  overflow: visible;
  border: 0;
  background: transparent;
  cursor: pointer;
}

.question-minimap__rail:focus-visible {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 2px;
}

.question-minimap__tick {
  position: absolute;
  left: 0;
  width: 8px;
  height: 2px;
  border-radius: 1px;
  background: var(--td-text-color-secondary);
  opacity: 0.4;
  transform: translateY(-50%);
  transform-origin: left center;
  transition: transform 120ms ease-out, background 120ms ease-out, opacity 120ms ease-out;
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
  left: 20px;
  width: 220px;
  max-height: min(280px, 40vh);
  padding: 6px 8px;
  overflow: hidden;
  scrollbar-width: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.06);
  transform: translateY(-50%);
  cursor: pointer;
}

.question-minimap__panel::-webkit-scrollbar {
  display: none;
}

.question-minimap__question,
.question-minimap__answer {
  margin: 0;
  font-size: 13px;
  font-weight: 400;
  line-height: 1.45;
}

.question-minimap__question {
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.question-minimap__answer {
  display: -webkit-box;
  margin-top: 4px;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  opacity: 0.8;
}

@media (prefers-reduced-motion: reduce) {
  .question-minimap__tick {
    transition: none;
  }
}
</style>
