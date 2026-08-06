<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'

const MIN = 0.3
const MAX = 8

const open = ref(false)
const scale = ref(1)
const tx = ref(0)
const ty = ref(0)
const stage = ref<HTMLElement>()

let dragging = false
let originX = 0
let originY = 0

function reset() {
  scale.value = 1
  tx.value = 0
  ty.value = 0
}

function close() {
  open.value = false
  document.documentElement.style.removeProperty('overflow')
}

async function show(source: SVGElement) {
  reset()
  open.value = true
  document.documentElement.style.overflow = 'hidden'
  await nextTick()
  const host = stage.value
  if (!host) return
  host.replaceChildren()

  // mermaid 的 svg 内联样式以 #id 作用域，且插件会对同 id 元素重新渲染，
  // 因此副本必须换一个 id 并同步改写内部样式表
  const clone = source.cloneNode(true) as SVGElement
  const oldId = source.id
  if (oldId) {
    const newId = `${oldId}-zoom`
    clone.id = newId
    clone.querySelectorAll('style').forEach((s) => {
      s.textContent = (s.textContent ?? '').split(`#${oldId}`).join(`#${newId}`)
    })
  }
  clone.removeAttribute('width')
  clone.removeAttribute('height')
  clone.style.maxWidth = 'none'
  clone.style.width = '100%'
  clone.style.height = '100%'
  host.appendChild(clone)
}

function onDocClick(e: MouseEvent) {
  const block = (e.target as HTMLElement | null)?.closest?.('.vp-doc .mermaid')
  if (!block) return
  const svg = block.querySelector('svg')
  if (svg) show(svg as SVGElement)
}

function onKey(e: KeyboardEvent) {
  if (!open.value) return
  if (e.key === 'Escape') close()
  if (e.key === '0') reset()
}

function zoomBy(factor: number) {
  scale.value = Math.min(MAX, Math.max(MIN, scale.value * factor))
}

function onWheel(e: WheelEvent) {
  e.preventDefault()
  zoomBy(e.deltaY < 0 ? 1.12 : 1 / 1.12)
}

function onDown(e: MouseEvent) {
  dragging = true
  originX = e.clientX - tx.value
  originY = e.clientY - ty.value
}

function onMove(e: MouseEvent) {
  if (!dragging) return
  tx.value = e.clientX - originX
  ty.value = e.clientY - originY
}

function onUp() {
  dragging = false
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onKey)
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('mousemove', onMove)
  window.removeEventListener('mouseup', onUp)
  document.documentElement.style.removeProperty('overflow')
})
</script>

<template>
  <Teleport to="body">
    <Transition name="zoom-fade">
      <div v-if="open" class="zoom" @wheel="onWheel" @mousedown.self="close">
        <div
          ref="stage"
          class="zoom-stage"
          :style="{ transform: `translate(${tx}px, ${ty}px) scale(${scale})` }"
          @mousedown.stop="onDown"
        />

        <div class="zoom-bar" @mousedown.stop>
          <button title="缩小" @click="zoomBy(1 / 1.35)">−</button>
          <span class="zoom-level">{{ Math.round(scale * 100) }}%</span>
          <button title="放大" @click="zoomBy(1.35)">+</button>
          <span class="zoom-sep" />
          <button class="zoom-word" @click="reset">重置</button>
          <button class="zoom-word" @click="close">关闭</button>
        </div>

        <p class="zoom-hint">滚轮缩放 · 拖拽平移 · Esc 关闭</p>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.zoom {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--wk-paper);
  cursor: zoom-out;
  overscroll-behavior: contain;
}

.zoom-stage {
  width: min(1400px, 88vw);
  height: 82vh;
  cursor: grab;
  transform-origin: center center;
}

.zoom-stage:active {
  cursor: grabbing;
}

.zoom-bar {
  position: fixed;
  bottom: 28px;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 8px;
  border: 1px solid var(--wk-rule);
  border-radius: 3px;
  background: var(--wk-paper);
  box-shadow: 0 6px 22px rgba(16, 31, 56, 0.08);
  cursor: default;
}

.zoom-bar button {
  min-width: 30px;
  height: 28px;
  padding: 0 8px;
  border-radius: 2px;
  font-size: 15px;
  line-height: 1;
  color: var(--wk-ink-soft);
  transition: background-color 0.18s ease, color 0.18s ease;
}

.zoom-bar button:hover {
  background: var(--wk-paper-2);
  color: var(--wk-ink);
}

.zoom-word {
  font-size: 12.5px !important;
}

.zoom-level {
  min-width: 46px;
  text-align: center;
  font-family: var(--wk-font-mono);
  font-size: 11px;
  color: var(--wk-ink-mute);
}

.zoom-sep {
  width: 1px;
  height: 16px;
  margin: 0 4px;
  background: var(--wk-rule);
}

.zoom-hint {
  position: fixed;
  top: 26px;
  left: 50%;
  transform: translateX(-50%);
  margin: 0;
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  letter-spacing: 0.14em;
  color: var(--wk-ink-mute);
}

.zoom-fade-enter-active,
.zoom-fade-leave-active {
  transition: opacity 0.18s ease;
}

.zoom-fade-enter-from,
.zoom-fade-leave-to {
  opacity: 0;
}
</style>
