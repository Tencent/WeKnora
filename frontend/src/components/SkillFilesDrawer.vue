<template>
  <teleport to="body">
    <div
      v-if="visible"
      class="skill-files-drawer-resize-handle"
      :class="{ 'skill-files-drawer-resize-handle--active': resizing }"
      :style="{ right: `${drawerWidth}px`, '--skill-files-drawer-travel': `${drawerWidth}px` }"
      role="separator"
      aria-orientation="vertical"
      @mousedown.prevent="onResizeStart"
    >
      <div class="skill-files-drawer-resize-line" />
    </div>
  </teleport>
  <t-drawer
    :visible="visible"
    :z-index="2600"
    :size="`${drawerWidth}px`"
    attach="body"
    :close-btn="false"
    :footer="false"
    :header="false"
    :show-overlay="true"
    :close-on-overlay-click="true"
    destroy-on-close
    placement="right"
    :class="['skill-files-drawer', { 'skill-files-drawer--resizing': resizing }]"
    @close="onClose"
  >
    <div class="skill-files-drawer__shell">
      <header class="skill-files-drawer__head">
        <div class="skill-files-drawer__head-icon">
          <t-icon name="folder" />
        </div>
        <div class="skill-files-drawer__head-text">
          <div class="skill-files-drawer__title">{{ skillName }}</div>
          <div class="skill-files-drawer__subtitle">{{ $t('settings.sandbox.skillFilesTitle') }}</div>
        </div>
        <button
          type="button"
          class="skill-files-drawer__close"
          :title="$t('common.close')"
          :aria-label="$t('common.close')"
          @click="onClose"
        >
          <t-icon name="close" size="16px" />
        </button>
      </header>
      <SkillFilesPanel
        v-if="visible && configId && skillId"
        :config-id="configId"
        :skill-id="skillId"
        :drawer-width="drawerWidth"
      />
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import SkillFilesPanel from '@/components/SkillFilesPanel.vue'

const WIDTH_KEY = 'skill-files-drawer:width'
const DEFAULT_WIDTH = 720
const MIN_WIDTH = 480

const props = defineProps<{
  visible: boolean
  configId: string
  skillId: string
  skillName: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const drawerWidth = ref(DEFAULT_WIDTH)
const resizing = ref(false)

let resizeStartX = 0
let resizeStartWidth = 0

function maxWidth() {
  return Math.min(1200, Math.max(MIN_WIDTH, Math.floor(window.innerWidth * 0.92)))
}

function clampWidth(width: number) {
  return Math.max(MIN_WIDTH, Math.min(maxWidth(), Math.round(width)))
}

function loadWidth() {
  try {
    const raw = localStorage.getItem(WIDTH_KEY)
    const parsed = raw ? parseInt(raw, 10) : NaN
    if (!Number.isNaN(parsed)) drawerWidth.value = clampWidth(parsed)
  } catch {
    /* ignore */
  }
}

function persistWidth() {
  try {
    localStorage.setItem(WIDTH_KEY, String(drawerWidth.value))
  } catch {
    /* ignore */
  }
}

function onResizeStart(e: MouseEvent) {
  resizing.value = true
  resizeStartX = e.clientX
  resizeStartWidth = drawerWidth.value
  document.addEventListener('mousemove', onResizeMove)
  document.addEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onResizeMove(e: MouseEvent) {
  drawerWidth.value = clampWidth(resizeStartWidth + (resizeStartX - e.clientX))
}

function onResizeEnd() {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  resizing.value = false
  persistWidth()
}

function cleanupResize() {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  resizing.value = false
}

function onWindowResize() {
  drawerWidth.value = clampWidth(drawerWidth.value)
}

function onClose() {
  emit('update:visible', false)
}

watch(
  () => props.visible,
  (open) => {
    if (open) loadWidth()
  },
)

onMounted(() => {
  loadWidth()
  window.addEventListener('resize', onWindowResize, { passive: true })
})

onUnmounted(() => {
  window.removeEventListener('resize', onWindowResize)
  cleanupResize()
})
</script>

<style lang="less" scoped>
.skill-files-drawer__shell {
  display: flex;
  flex-direction: column;
  flex: 1;
  height: 100%;
  min-height: 0;
  width: 100%;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.skill-files-drawer__head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
  padding: 14px 18px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.skill-files-drawer__head-icon {
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

.skill-files-drawer__head-text {
  min-width: 0;
  flex: 1;
}

.skill-files-drawer__title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-files-drawer__subtitle {
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
}

.skill-files-drawer__close {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}
</style>

<style lang="less">
.t-drawer.skill-files-drawer .t-drawer__content-wrapper,
.t-drawer.skill-files-drawer .t-drawer__content {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.t-drawer.skill-files-drawer .t-drawer__body {
  padding: 0 !important;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.t-drawer.skill-files-drawer--resizing .t-drawer__content,
.t-drawer.skill-files-drawer--resizing .t-drawer__content-wrapper {
  transition: none !important;
}

.skill-files-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  cursor: col-resize;
  z-index: 2601;
  display: flex;
  align-items: center;
  justify-content: center;
  animation: skill-files-drawer-resize-handle-in 0.28s cubic-bezier(0.38, 0, 0.24, 1) both;
}

@keyframes skill-files-drawer-resize-handle-in {
  from {
    transform: translateX(var(--skill-files-drawer-travel));
  }

  to {
    transform: translateX(0);
  }
}

.skill-files-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.skill-files-drawer-resize-handle:hover .skill-files-drawer-resize-line,
.skill-files-drawer-resize-handle--active .skill-files-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}
</style>
