<template>
  <Teleport to="body">
    <div v-if="position" ref="bubble" class="assistant-selection-bubble" data-assistant-ignore role="toolbar"
      :aria-label="t('assistant.bubble.label')" :style="{ left: `${position.left}px`, top: `${position.top}px` }"
      @mousedown.prevent>
      <button type="button" class="assistant-selection-bubble__btn is-primary" @click="run('ask')">
        <t-icon name="compass" />{{ t('assistant.bubble.ask') }}
      </button>
      <span class="assistant-selection-bubble__divider" aria-hidden="true" />
      <button v-for="action in QUICK_ACTIONS" :key="action" type="button" class="assistant-selection-bubble__btn"
        @click="run(action)">
        {{ t(`assistant.bubble.${action}`) }}
      </button>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import { selectedTextControl } from '@/utils/pageAwareness'

const QUICK_ACTIONS = ['polish', 'rewrite', 'translate', 'continue'] as const
type Action = 'ask' | typeof QUICK_ACTIONS[number]

const { t } = useI18n()
const assistant = useAssistantStore()
const bubble = ref<HTMLElement | null>(null)
const position = ref<{ left: number; top: number } | null>(null)

// Where the pointer let go of the selection, to put the bar beside it.
let lastPointer = { x: 0, y: 0, at: 0 }
const MARGIN = 8

const hide = () => { position.value = null }

async function place() {
  const control = selectedTextControl()
  if (!control) return hide()
  let x: number
  let y: number
  if (Date.now() - lastPointer.at < 1500) {
    ({ x, y } = lastPointer)
  } else {
    const box = control.getBoundingClientRect()
    x = box.right - 120
    y = box.top + 12
  }
  position.value = { left: x, top: y - 44 }
  await nextTick()
  const width = bubble.value?.offsetWidth || 320
  const height = bubble.value?.offsetHeight || 34
  const top = y - height - 10 < MARGIN ? y + 16 : y - height - 10
  const left = Math.min(Math.max(MARGIN, x - width / 2), window.innerWidth - width - MARGIN)
  position.value = { left, top }
}

// Only a selection in a field the assistant can write to gets the bar.
watch(() => assistant.pageSelection, selection => {
  if (!selection?.ref || !assistant.pageAccess) hide()
  else void place()
})

function run(action: Action) {
  hide()
  if (action === 'ask') assistant.focusComposer()
  else assistant.ask(t(`assistant.bubble.prompts.${action}`))
}

const inside = (event: Event) => !!bubble.value && event.target instanceof Node && bubble.value.contains(event.target)
const onPointerUp = (event: PointerEvent) => {
  if (!inside(event)) lastPointer = { x: event.clientX, y: event.clientY, at: Date.now() }
}
const onPointerDown = (event: PointerEvent) => {
  if (!inside(event)) hide()
}
// Extending the selection from the keyboard keeps the bar; typing does not.
const KEEP_KEYS = new Set(['Shift', 'Meta', 'Control', 'Alt', 'ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'])
const onKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' || !KEEP_KEYS.has(event.key)) hide()
}

onMounted(() => {
  document.addEventListener('pointerup', onPointerUp, true)
  document.addEventListener('pointerdown', onPointerDown, true)
  document.addEventListener('keydown', onKeyDown, true)
  document.addEventListener('scroll', hide, true)
  window.addEventListener('resize', hide)
})
onUnmounted(() => {
  document.removeEventListener('pointerup', onPointerUp, true)
  document.removeEventListener('pointerdown', onPointerDown, true)
  document.removeEventListener('keydown', onKeyDown, true)
  document.removeEventListener('scroll', hide, true)
  window.removeEventListener('resize', hide)
})
</script>

<style lang="less" scoped>
.assistant-selection-bubble {
  position: fixed;
  z-index: 5000;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  animation: assistant-selection-bubble-in var(--app-motion-fast, 120ms) ease-out;
}

@keyframes assistant-selection-bubble-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}

.assistant-selection-bubble__btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 8px;
  border: 0;
  border-radius: var(--td-radius-medium);
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: var(--app-text-sm);
  white-space: nowrap;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-primary {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.assistant-selection-bubble__divider {
  width: 1px;
  height: 16px;
  margin: 0 2px;
  background: var(--td-component-stroke);
}

@media (prefers-reduced-motion: reduce) {
  .assistant-selection-bubble {
    animation: none;
  }
}
</style>
