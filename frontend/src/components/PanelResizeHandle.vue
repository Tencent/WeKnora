<template>
  <div class="panel-resize-handle" :class="[`panel-resize-handle--${edge}`, { 'is-dragging': dragging }]"
    role="separator" aria-orientation="vertical" :aria-label="label" tabindex="0"
    :aria-valuenow="value" :aria-valuemin="min" :aria-valuemax="max"
    @pointerdown="start" @pointermove="move" @pointerup="finish" @pointercancel="finish"
    @lostpointercapture="finish" @keydown="onKeydown">
    <span class="panel-resize-handle__grip" aria-hidden="true" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

defineProps<{ edge: 'left' | 'right'; label: string; value: number; min: number; max: number }>()
const emit = defineEmits<{ start: []; resize: [delta: number, keyboard: boolean]; end: [] }>()
const dragging = ref(false)
let drag: { element: HTMLElement; pointerId: number; x: number; scale: number; cursor: string; userSelect: string } | null = null

function start(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0 || drag) return
  event.preventDefault()
  const element = event.currentTarget as HTMLElement
  element.setPointerCapture(event.pointerId)
  drag = {
    element, pointerId: event.pointerId, x: event.clientX,
    // The app's font-size setting uses CSS zoom; pointer coordinates are visual pixels.
    scale: element.getBoundingClientRect().width / element.offsetWidth || 1,
    cursor: document.body.style.cursor, userSelect: document.body.style.userSelect,
  }
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  dragging.value = true
  emit('start')
}

function move(event: PointerEvent) {
  if (!drag || event.pointerId !== drag.pointerId) return
  emit('resize', (event.clientX - drag.x) / drag.scale, false)
}

function finish(event?: PointerEvent) {
  if (!drag || (event && event.pointerId !== drag.pointerId)) return
  const previous = drag
  drag = null
  if (previous.element.hasPointerCapture(previous.pointerId)) previous.element.releasePointerCapture(previous.pointerId)
  document.body.style.cursor = previous.cursor
  document.body.style.userSelect = previous.userSelect
  dragging.value = false
  emit('end')
}

function onKeydown(event: KeyboardEvent) {
  if (drag || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return
  event.preventDefault()
  emit('start')
  emit('resize', event.key === 'ArrowRight' ? 20 : -20, true)
  emit('end')
}

onBeforeUnmount(() => finish())
</script>

<style scoped lang="less">
.panel-resize-handle {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 8px;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: col-resize;
  touch-action: none;
  outline: none;

  &--left { left: 0; --grip-offset: -12px; }
  &--right { right: 0; --grip-offset: 12px; }

  // Bridge the gap around the floating grip without covering the neighboring
  // scrollbar along the entire edge.
  &::before {
    content: '';
    position: absolute;
    top: 50%;
    left: 50%;
    width: 52px;
    height: 80px;
    transform: translate(-50%, -50%);
  }

  &__grip {
    flex-shrink: 0;
    width: 6px;
    height: 56px;
    border-radius: 3px;
    background: color-mix(in srgb, var(--td-text-color-secondary) 28%, transparent);
    opacity: 0;
    transform: translateX(var(--grip-offset)) scale(0.94);
    transition: opacity var(--app-motion-fast) ease, transform var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease;
    pointer-events: none;
  }

  &:hover &__grip,
  &:focus-visible &__grip,
  &.is-dragging &__grip {
    opacity: 1;
    transform: translateX(var(--grip-offset)) scale(1);
  }

  &.is-dragging &__grip {
    background: color-mix(in srgb, var(--td-text-color-secondary) 42%, transparent);
  }
}
</style>
