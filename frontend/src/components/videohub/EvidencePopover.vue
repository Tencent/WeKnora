<template>
  <div
    class="video-evidence-popover__anchor"
    aria-haspopup="dialog"
    :aria-expanded="visible"
    @keydown.esc="close"
  >
    <slot :open="open" :close="close" :visible="visible" />
  </div>
  <Teleport to="body">
    <div v-if="visible" class="video-evidence-popover-layer">
      <div ref="popover" class="video-evidence-popover" role="dialog" aria-label="视频内容出处" @click.stop>
        <div class="video-evidence-popover__title">
          <span>原文出处</span>
          <span>共 {{ evidence.length }} 条</span>
        </div>
        <div class="video-evidence-popover__list">
          <div v-for="item in evidence" :key="item.chunkId" class="video-evidence-popover__item">
            <div class="video-evidence-popover__time">{{ item.timestamp }}</div>
            <blockquote>{{ item.transcriptSnippet }}</blockquote>
            <button type="button" class="video-evidence-popover__timestamp" @click="seek(item.startSeconds)">
              <t-icon name="play-circle" size="14px" />
              <span>定位到视频</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import type { SummaryEvidence } from '@/types/videohub'

const props = defineProps<{ evidence: SummaryEvidence[] }>()
const emit = defineEmits<{ seek: [seconds: number] }>()
const visible = ref(false)
const popover = ref<HTMLElement | null>(null)
const anchor = ref<HTMLElement | null>(null)
const evidence = computed(() => props.evidence)

defineSlots<{
  default(props: { open: (event: MouseEvent) => void; close: () => void; visible: boolean }): unknown
}>()

function open(event: MouseEvent) {
  const button = event.currentTarget
  if (!(button instanceof HTMLElement)) return

  if (visible.value && anchor.value === button) {
    close()
    return
  }

  anchor.value = button
  visible.value = true
  addDocumentListeners()
  void nextTick(positionPopover)
}

function positionPopover() {
  const panel = popover.value
  const target = anchor.value
  if (!panel || !target) return

  panel.style.visibility = 'hidden'
  panel.style.left = '0px'
  panel.style.top = '0px'

  const targetRect = target.getBoundingClientRect()
  const panelRect = panel.getBoundingClientRect()
  const padding = 16
  const gap = 8
  const candidates = [
    { left: targetRect.right - panelRect.width, top: targetRect.bottom + gap },
    { left: targetRect.right - panelRect.width, top: targetRect.top - panelRect.height - gap },
    { left: targetRect.left - panelRect.width - gap, top: targetRect.top },
    { left: targetRect.right + gap, top: targetRect.top },
  ]
  const fits = (candidate: { left: number; top: number }) => (
    candidate.left >= padding &&
    candidate.top >= padding &&
    candidate.left + panelRect.width <= window.innerWidth - padding &&
    candidate.top + panelRect.height <= window.innerHeight - padding
  )
  const candidate = candidates.find(fits) || candidates[0]
  const left = Math.max(padding, Math.min(candidate.left, window.innerWidth - panelRect.width - padding))
  const top = Math.max(padding, Math.min(candidate.top, window.innerHeight - panelRect.height - padding))

  panel.style.left = `${left}px`
  panel.style.top = `${top}px`
  panel.style.visibility = 'visible'
}

function addDocumentListeners() {
  document.addEventListener('pointerdown', handleDocumentPointerDown, true)
  document.addEventListener('keydown', handleDocumentKeydown, true)
  window.addEventListener('resize', positionPopover)
  window.addEventListener('scroll', positionPopover, true)
}

function removeDocumentListeners() {
  document.removeEventListener('pointerdown', handleDocumentPointerDown, true)
  document.removeEventListener('keydown', handleDocumentKeydown, true)
  window.removeEventListener('resize', positionPopover)
  window.removeEventListener('scroll', positionPopover, true)
}

function handleDocumentPointerDown(event: PointerEvent) {
  const target = event.target
  if (target instanceof Node && (popover.value?.contains(target) || anchor.value?.contains(target))) return
  close()
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') close()
}

function close() {
  visible.value = false
  anchor.value = null
  removeDocumentListeners()
}

function seek(seconds: number) {
  emit('seek', seconds)
  close()
}

onBeforeUnmount(close)
</script>

<style>
.video-evidence-popover-layer { position: fixed; inset: 0; z-index: 2147483647; pointer-events: none; }
.video-evidence-popover__anchor { border-radius: var(--td-radius-medium); outline: none; }
.video-evidence-popover { position: fixed; width: min(360px, calc(100vw - 32px)); max-width: calc(100vw - 32px); max-height: calc(100vh - 32px); box-sizing: border-box; overflow: auto; padding: calc(var(--td-comp-margin-s) * 2); border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--rounded-popup, 10px); background: var(--color-bg-popup, var(--td-bg-color-container)); box-shadow: var(--shadow-popup); backdrop-filter: blur(20px) saturate(180%); color: var(--td-text-color-primary); pointer-events: auto; }
.video-evidence-popover__title { display: flex; align-items: center; gap: var(--td-comp-margin-s); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); font-weight: 600; }
.video-evidence-popover__list { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); max-height: 320px; overflow-y: auto; margin-top: calc(var(--td-comp-margin-s) * 1.5); }
.video-evidence-popover__item + .video-evidence-popover__item { padding-top: calc(var(--td-comp-margin-s) * 1.5); border-top: var(--border-width-hairline, .5px) solid var(--td-component-stroke); }
.video-evidence-popover blockquote { margin: calc(var(--td-comp-margin-s) / 2) 0 var(--td-comp-margin-s); padding: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-medium); line-height: 1.65; }
.video-evidence-popover__footer { display: flex; align-items: center; justify-content: flex-end; }
.video-evidence-popover__time { color: var(--td-brand-color); font-family: monospace; font-size: var(--td-font-size-body-small); font-weight: 500; }
.video-evidence-popover__timestamp { display: inline-flex; align-items: center; gap: calc(var(--td-comp-margin-s) / 2); padding: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); border: 0; border-radius: var(--td-radius-medium); background: var(--td-brand-color-light); color: var(--td-brand-color); font: inherit; font-size: var(--td-font-size-body-small); cursor: pointer; }
.video-evidence-popover__timestamp:hover { background: var(--td-brand-color-focus); color: var(--td-brand-color-hover); }
</style>
