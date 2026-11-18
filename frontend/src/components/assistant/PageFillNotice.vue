<template>
  <Teleport to="body">
    <div v-if="notice && undoable && box" class="page-fill-notice" data-assistant-ignore role="status"
      :style="{ left: `${box.left}px`, top: `${box.top}px` }" @mouseenter="pause" @mouseleave="resume">
      <t-icon name="ai-edit" class="page-fill-notice__icon" />
      <span class="page-fill-notice__text">{{ t('assistant.fillNotice.title', { count: notice.count }) }}</span>
      <button type="button" class="page-fill-notice__btn" :disabled="undoing" @click="undo">
        {{ t('assistant.fillNotice.undo') }}
      </button>
      <button type="button" class="page-fill-notice__close" :aria-label="t('assistant.fillNotice.dismiss')"
        @click="dismiss">
        <t-icon name="close" />
      </button>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { canUndoFill, lastPageFill, undoFill, type PageFillNotice } from '@/utils/pageFill'

const SHOW_MS = 10_000
const WIDTH_GUESS = 240

const { t } = useI18n()
const notice = ref<PageFillNotice | null>(null)
const box = ref<{ left: number; top: number } | null>(null)
const undoing = ref(false)
const undoable = computed(() => !!notice.value && canUndoFill(notice.value.key))

let timer = 0
let frame = 0

// Sits on the top edge of the field it changed, following it as the page scrolls.
function track() {
  const anchor = notice.value?.anchor.deref()
  if (!anchor?.isConnected) return dismiss()
  const rect = anchor.getBoundingClientRect()
  const onScreen = rect.bottom > 0 && rect.top < window.innerHeight && rect.width > 0
  if (!onScreen) {
    box.value = null
  } else {
    const top = rect.top - 36 >= 4 ? rect.top - 36 : rect.top + 6
    const left = Math.max(8, Math.min(rect.right - WIDTH_GUESS, window.innerWidth - WIDTH_GUESS - 8))
    box.value = { left, top }
  }
  frame = requestAnimationFrame(track)
}

function resume() {
  window.clearTimeout(timer)
  timer = window.setTimeout(dismiss, SHOW_MS)
}
function pause() {
  window.clearTimeout(timer)
}
function dismiss() {
  window.clearTimeout(timer)
  cancelAnimationFrame(frame)
  notice.value = null
  box.value = null
}

watch(lastPageFill, next => {
  dismiss()
  if (!next) return
  notice.value = next
  track()
  resume()
})

// Undone from the chat card: nothing left to offer here.
watch(undoable, can => {
  if (!can && notice.value) dismiss()
})

async function undo() {
  const current = notice.value
  if (!current) return
  undoing.value = true
  try {
    const { skipped } = await undoFill(current.key)
    if (skipped > 0) MessagePlugin.warning(t('agentStream.pageEdit.undoSkipped', { count: skipped }))
  } finally {
    undoing.value = false
    dismiss()
  }
}

onUnmounted(dismiss)
</script>

<style lang="less" scoped>
.page-fill-notice {
  position: fixed;
  z-index: 5000;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 4px 0 10px;
  border: 1px solid var(--td-brand-color-focus);
  border-radius: var(--td-radius-large);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  font-size: var(--app-text-sm);
  white-space: nowrap;
  animation: page-fill-notice-in var(--app-motion-fast, 120ms) ease-out;
}

@keyframes page-fill-notice-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}

.page-fill-notice__icon {
  color: var(--td-brand-color);
  font-size: var(--app-text-base);
}

.page-fill-notice__text {
  color: var(--td-text-color-primary);
}

.page-fill-notice__btn,
.page-fill-notice__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 24px;
  border: 0;
  border-radius: var(--td-radius-medium);
  background: transparent;
  font: inherit;
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
  }
}

.page-fill-notice__btn {
  padding: 0 8px;
  color: var(--td-brand-color);
  font-weight: 500;
}

.page-fill-notice__close {
  width: 24px;
  color: var(--td-text-color-placeholder);
}

@media (prefers-reduced-motion: reduce) {
  .page-fill-notice {
    animation: none;
  }
}
</style>
