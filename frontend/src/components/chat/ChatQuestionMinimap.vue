<template>
  <nav
    v-if="visible"
    ref="rootRef"
    class="question-minimap"
    :style="{ right: `${scrollbarGutterPx + RAIL_INSET_PX}px`, height: `${trackHeight}px` }"
    :aria-label="t('chat.questionMinimapAriaLabel')"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
  >
    <button
      class="question-minimap__rail"
      type="button"
      tabindex="0"
      :aria-label="t('chat.questionMinimapAriaLabel')"
      aria-haspopup="listbox"
      :aria-controls="listboxId"
      :aria-expanded="panelOpen"
      :aria-activedescendant="activeDescendantId"
      @click="handleRailClick"
      @keydown="handleRailKeydown"
    >
      <span
        v-for="tick in ticks"
        :key="tick.id"
        class="question-minimap__tick"
        :class="{ 'question-minimap__tick--active': tick.id === activeId || tick.id === hoveredId }"
        :style="{ top: `${tick.yPx}px` }"
      />
    </button>

    <div
      v-if="panelOpen"
      class="question-minimap__bridge"
      aria-hidden="true"
    />

    <section
      v-if="panelOpen"
      ref="panelRef"
      class="question-minimap__panel"
      :aria-label="t('chat.questionMinimapTitle')"
    >
      <div
        :id="listboxId"
        class="question-minimap__list"
        role="listbox"
        :aria-label="t('chat.questionMinimapTitle')"
      >
        <button
          v-for="(question, index) in questions"
          :key="question.id"
          :id="optionId(question.id)"
          class="question-minimap__row"
          :class="{
            'question-minimap__row--keyboard': index === keyboardIndex,
          }"
          type="button"
          role="option"
          :aria-selected="panelOpen && index === keyboardIndex"
          :aria-disabled="!anchoredIds.has(question.id)"
          :disabled="!anchoredIds.has(question.id)"
          :title="displayText(question.content)"
          :data-keyboard="index === keyboardIndex ? 'true' : undefined"
          @click="handleQuestionClick(question.id)"
          @mouseenter="hoveredId = question.id"
          @mouseleave="hoveredId = null"
        >
          {{ displayText(question.content) }}
        </button>
      </div>
    </section>
  </nav>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChatQuestionMinimap } from '@/composables/useChatQuestionMinimap'
import {
  questionDisplayText,
  type ChatMessageLike,
} from '@/utils/chatQuestionMinimap'

const CLOSE_DELAY_MS = 150
const RAIL_INSET_PX = 16

const props = defineProps<{
  scrollContainer: HTMLElement | null
  messages: ChatMessageLike[]
}>()

const emit = defineEmits<{
  (event: 'jump', messageId: string): void
}>()

const { t } = useI18n()
const rootRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const hoverOpen = ref(false)
const pinnedOpen = ref(false)
const hoveredId = ref<string | null>(null)
const keyboardIndex = ref(-1)
const isCoarsePointer = ref(false)

const {
  visible,
  questions,
  ticks,
  activeId,
  anchoredIds,
  scrollbarGutterPx,
  trackHeight,
} = useChatQuestionMinimap({
  scrollContainer: toRef(props, 'scrollContainer'),
  messages: toRef(props, 'messages'),
})

const listboxId = 'question-minimap-list'
const panelOpen = computed(() => hoverOpen.value || pinnedOpen.value)
const anchoredQuestions = computed(() => (
  questions.value.filter((question) => anchoredIds.value.has(question.id))
))
const optionId = (id: string) => `question-minimap-option-${id}`
const activeDescendantId = computed(() => {
  const question = questions.value[keyboardIndex.value]
  return panelOpen.value && question ? optionId(question.id) : undefined
})

let closeTimer: number | null = null

const displayText = (content: string) => (
  questionDisplayText(content, t('chat.questionMinimapAttachmentPlaceholder'))
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
  keyboardIndex.value = -1
}

const scheduleClose = () => {
  clearCloseTimer()
  closeTimer = window.setTimeout(() => {
    hoverOpen.value = false
    hoveredId.value = null
    closeTimer = null
  }, CLOSE_DELAY_MS)
}

const scrollKeyboardRowIntoView = async () => {
  if (!panelOpen.value) return

  await nextTick()
  const keyboardRow = panelRef.value?.querySelector<HTMLElement>('[data-keyboard="true"]')
  keyboardRow?.scrollIntoView({ block: 'nearest' })
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

const handleRailClick = () => {
  if (!isCoarsePointer.value) return

  clearCloseTimer()
  pinnedOpen.value = !pinnedOpen.value
  hoverOpen.value = false
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
  void scrollKeyboardRowIntoView()
}

const jumpKeyboardQuestion = () => {
  const question = questions.value[keyboardIndex.value]
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

watch(panelOpen, (open) => {
  if (open && keyboardIndex.value >= 0) {
    void scrollKeyboardRowIntoView()
  }
})

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
  right: 0;
  z-index: 11;
  display: flex;
  flex-direction: row-reverse;
  align-items: center;
  transform: translateY(-50%);
  pointer-events: none;
}

.question-minimap__rail,
.question-minimap__bridge,
.question-minimap__panel {
  pointer-events: auto;
}

.question-minimap__rail {
  position: relative;
  width: 12px;
  height: 100%;
  padding: 0;
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
  left: 2px;
  width: 8px;
  height: 2px;
  border-radius: 1px;
  background: var(--td-text-color-secondary);
  opacity: 0.55;
  transform: translateY(-50%);
}

.question-minimap__tick--active {
  background: var(--td-brand-color);
  opacity: 1;
}

.question-minimap__bridge {
  align-self: stretch;
  width: 8px;
}

.question-minimap__panel {
  width: 240px;
  max-height: min(360px, 50vh);
  overflow-y: auto;
  scrollbar-width: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.question-minimap__panel::-webkit-scrollbar {
  display: none;
}

.question-minimap__list {
  padding: 6px;
}

.question-minimap__row {
  display: block;
  width: 100%;
  padding: 7px 8px;
  overflow: hidden;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font: inherit;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.question-minimap__row:hover {
  color: var(--td-brand-color);
}

.question-minimap__row--keyboard:not(:hover) {
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
}

.question-minimap__row:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
