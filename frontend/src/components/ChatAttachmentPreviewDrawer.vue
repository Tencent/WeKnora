<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import DocumentPreview from '@/components/document-preview.vue'
import { useChatAttachmentPreviewDrawer } from '@/composables/useChatAttachmentPreviewDrawer'

const drawer = useChatAttachmentPreviewDrawer()

const MAIN_DRAWER_WIDTH_KEY = 'weknora-chat-attachment-drawer-width'
const MAIN_DRAWER_DEFAULT_WIDTH = 654
const MAIN_DRAWER_MIN_WIDTH = 480

const mainDrawerWidth = ref(MAIN_DRAWER_DEFAULT_WIDTH)
const mainDrawerResizing = ref(false)

let mainResizeStartX = 0
let mainResizeStartWidth = 0

const visible = computed(() => drawer?.visible.value ?? false)
const target = computed(() => drawer?.target.value ?? null)

function mainDrawerMaxWidth() {
  return Math.min(1600, Math.max(MAIN_DRAWER_MIN_WIDTH, Math.floor(window.innerWidth * 0.95)))
}

function clampMainDrawerWidth(width: number) {
  return Math.max(MAIN_DRAWER_MIN_WIDTH, Math.min(mainDrawerMaxWidth(), width))
}

function loadMainDrawerWidth() {
  try {
    const raw = localStorage.getItem(MAIN_DRAWER_WIDTH_KEY)
    const parsed = raw ? parseInt(raw, 10) : NaN
    if (!Number.isNaN(parsed)) {
      mainDrawerWidth.value = clampMainDrawerWidth(parsed)
    }
  } catch {
    /* ignore */
  }
}

function onMainDrawerResizeStart(e: MouseEvent) {
  mainDrawerResizing.value = true
  mainResizeStartX = e.clientX
  mainResizeStartWidth = mainDrawerWidth.value
  document.addEventListener('mousemove', onMainDrawerResizeMove)
  document.addEventListener('mouseup', onMainDrawerResizeEnd)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onMainDrawerResizeMove(e: MouseEvent) {
  const delta = mainResizeStartX - e.clientX
  mainDrawerWidth.value = clampMainDrawerWidth(mainResizeStartWidth + delta)
}

function onMainDrawerResizeEnd() {
  document.removeEventListener('mousemove', onMainDrawerResizeMove)
  document.removeEventListener('mouseup', onMainDrawerResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  mainDrawerResizing.value = false
  try {
    localStorage.setItem(MAIN_DRAWER_WIDTH_KEY, String(mainDrawerWidth.value))
  } catch {
    /* ignore */
  }
}

function cleanupMainDrawerResize() {
  document.removeEventListener('mousemove', onMainDrawerResizeMove)
  document.removeEventListener('mouseup', onMainDrawerResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  mainDrawerResizing.value = false
}

function onWindowResize() {
  mainDrawerWidth.value = clampMainDrawerWidth(mainDrawerWidth.value)
}

function close() {
  drawer?.close()
}

onMounted(() => {
  loadMainDrawerWidth()
  window.addEventListener('resize', onWindowResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', onWindowResize)
  cleanupMainDrawerResize()
})
</script>

<template>
  <teleport to="body">
    <div
      v-if="mainDrawerResizing"
      class="chat-attachment-drawer-resize-overlay"
      aria-hidden="true"
    />
    <div
      v-if="visible"
      class="chat-attachment-drawer-resize-handle"
      :class="{ 'chat-attachment-drawer-resize-handle--active': mainDrawerResizing }"
      :style="{ right: `${mainDrawerWidth}px` }"
      role="separator"
      aria-orientation="vertical"
      @mousedown.prevent="onMainDrawerResizeStart"
    >
      <div class="chat-attachment-drawer-resize-line" />
    </div>
  </teleport>

  <t-drawer
    :visible="visible"
    :z-index="2000"
    :size="`${mainDrawerWidth}px`"
    attach="body"
    :close-btn="true"
    :footer="false"
    :class="['chat-attachment-preview-drawer', { 'chat-attachment-preview-drawer--resizing': mainDrawerResizing }]"
    @close="close"
  >
    <template #header>
      <div class="chat-attachment-drawer-header">
        <div class="chat-attachment-drawer-header-icon">
          <t-icon name="file" />
        </div>
        <div class="chat-attachment-drawer-header-text">
          <div class="chat-attachment-drawer-header-title">{{ target?.fileName || '' }}</div>
        </div>
      </div>
    </template>

    <section v-if="target" class="chat-attachment-drawer-body">
      <DocumentPreview
        :session-id="target.sessionId"
        :attachment-id="target.attachmentId"
        :file-type="target.fileType"
        :file-name="target.fileName"
        :active="visible"
        fill-height
      />
    </section>
  </t-drawer>
</template>

<style scoped lang="less">
:deep(.t-drawer__header) {
  font-weight: normal;
}

.chat-attachment-drawer-header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  width: 100%;
  padding-right: 32px;
}

.chat-attachment-drawer-header-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(7, 192, 95, 0.1);
  color: var(--td-brand-color);
  font-size: 16px;
}

.chat-attachment-drawer-header-text {
  flex: 1 1 auto;
  min-width: 0;
}

.chat-attachment-drawer-header-title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-attachment-drawer-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.chat-attachment-drawer-resize-overlay {
  position: fixed;
  inset: 0;
  z-index: 2001;
  cursor: col-resize;
}

.chat-attachment-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  z-index: 2002;
  cursor: col-resize;
  display: flex;
  align-items: center;
  justify-content: center;
}

.chat-attachment-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.chat-attachment-drawer-resize-handle:hover .chat-attachment-drawer-resize-line,
.chat-attachment-drawer-resize-handle--active .chat-attachment-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}
</style>

<style lang="less">
.t-drawer.chat-attachment-preview-drawer {
  .t-drawer__content-wrapper,
  .t-drawer__content {
    height: 100%;
  }

  .t-drawer__header {
    padding: 14px 18px;
    border-bottom: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
  }

  .t-drawer__body {
    flex: 1;
    min-height: 0;
    padding: 12px 16px 16px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
}

.t-drawer.chat-attachment-preview-drawer--resizing .t-drawer__content {
  transition: none !important;
}

.t-drawer.chat-attachment-preview-drawer--resizing {
  .chat-attachment-drawer-body,
  .document-preview,
  iframe,
  .pdf-iframe {
    pointer-events: none;
    user-select: none;
  }
}
</style>
