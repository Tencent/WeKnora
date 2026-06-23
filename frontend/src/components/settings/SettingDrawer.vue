<template>
  <teleport to="body">
    <div v-if="drawerVisible && resizable" class="setting-drawer-resize-handle"
      :class="{ 'setting-drawer-resize-handle--active': drawerResizing }"
      :style="{ right: `${drawerWidthPx}px`, '--setting-drawer-travel': `${drawerWidthPx}px` }"
      role="separator" aria-orientation="vertical" @mousedown.prevent="onResizeStart">
      <div class="setting-drawer-resize-line" />
    </div>
  </teleport>
  <t-drawer v-model:visible="drawerVisible" :size="effectiveWidth" :z-index="2500" placement="right"
    attach="body" destroy-on-close
    :class="['setting-drawer', { 'setting-drawer--resizing': drawerResizing }]">
    <!--
      Custom header. We replace TDesign's default header so we can put a leading
      icon badge and an optional subtitle (description) right next to the title,
      keeping the body uncluttered. The close affordance is the slide-out drawer
      itself + the underlying overlay click — TDesign already wires those up,
      so we don't need a redundant X button.
    -->
    <template #header>
      <div class="setting-drawer__header">
        <div v-if="$slots.headerIcon || icon" class="setting-drawer__header-icon">
          <slot name="headerIcon">
            <t-icon v-if="icon" :name="icon" />
          </slot>
        </div>
        <div class="setting-drawer__header-text">
          <div class="setting-drawer__title">{{ title }}</div>
          <div v-if="description || $slots.subtitle" class="setting-drawer__subtitle">
            <slot name="subtitle">{{ description }}</slot>
          </div>
        </div>
      </div>
    </template>

    <div class="setting-drawer__body">
      <slot />
    </div>
    <template v-if="!hideFooter" #footer>
      <div class="setting-drawer__footer">
        <div class="setting-drawer__footer-left">
          <slot name="footer-left" />
        </div>
        <div class="setting-drawer__footer-right">
          <t-button theme="default" variant="outline" @click="handleCancel">
            {{ cancelText || t('common.cancel') }}
          </t-button>
          <t-button theme="primary" :loading="confirmLoading" :disabled="confirmDisabled" @click="handleConfirm">
            {{ confirmText || t('common.save') }}
          </t-button>
        </div>
      </div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

interface Props {
  visible: boolean
  title: string
  description?: string
  /** Optional TDesign icon name shown as a leading badge in the header. */
  icon?: string
  /**
   * Initial width when the user has no persisted preference. Accepts any
   * CSS length string (e.g. "560px", "40%").
   */
  width?: string
  /**
   * Whether the drawer can be horizontally resized by dragging the visible
   * handle on its left edge (same affordance as doc-content drawer).
   */
  resizable?: boolean
  /** Min/max bounds for the drag-resize, in px. */
  minWidth?: number
  maxWidth?: number
  /**
   * localStorage key used to remember the user's chosen width. Set to '' to
   * disable persistence. Default key is namespaced per-consumer using the
   * drawer title.
   */
  storageKey?: string
  confirmLoading?: boolean
  confirmDisabled?: boolean
  confirmText?: string
  cancelText?: string
  hideFooter?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  description: '',
  icon: '',
  width: '560px',
  resizable: true,
  minWidth: 480,
  maxWidth: 1200,
  storageKey: '',
  confirmLoading: false,
  confirmDisabled: false,
  confirmText: '',
  cancelText: '',
  hideFooter: false
})

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()

// ---------- visibility ----------
const drawerVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

// ---------- width state ----------
// Storage key derives from the drawer title so different drawers (model
// editor vs MCP service vs web search provider) get independent widths.
// Callers can override via the `storageKey` prop when titles collide.
const resolvedStorageKey = computed(
  () => props.storageKey || `setting-drawer:width:${props.title || 'default'}`
)

const clampWidth = (n: number) =>
  Math.max(props.minWidth, Math.min(props.maxWidth, Math.round(n)))

const parseWidthToPx = (width: string) => {
  const n = parseInt(width, 10)
  return Number.isFinite(n) ? n : 560
}

const loadStoredWidth = (): number | null => {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(resolvedStorageKey.value)
    if (!raw) return null
    const n = Number(raw)
    if (!Number.isFinite(n)) return null
    return clampWidth(n)
  } catch {
    return null
  }
}

// User's persisted width (px) wins over the prop default.
const userWidthPx = ref<number | null>(loadStoredWidth())

const effectiveWidth = computed(() =>
  userWidthPx.value != null ? `${userWidthPx.value}px` : props.width
)

const drawerWidthPx = computed(() =>
  userWidthPx.value ?? parseWidthToPx(props.width)
)

const persistWidth = (width: number) => {
  const next = clampWidth(width)
  userWidthPx.value = next
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(resolvedStorageKey.value, String(next))
  } catch {
    // localStorage can throw in private mode / quota errors.
  }
}

// ---------- Custom drag-resize (visible handle, same as doc-content) ----------
const drawerResizing = ref(false)
let resizeStartX = 0
let resizeStartWidth = 0

function onResizeStart(e: MouseEvent) {
  drawerResizing.value = true
  resizeStartX = e.clientX
  resizeStartWidth = drawerWidthPx.value
  document.addEventListener('mousemove', onResizeMove)
  document.addEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onResizeMove(e: MouseEvent) {
  const delta = resizeStartX - e.clientX
  userWidthPx.value = clampWidth(resizeStartWidth + delta)
}

function onResizeEnd() {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  drawerResizing.value = false
  persistWidth(drawerWidthPx.value)
}

function cleanupResize() {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  drawerResizing.value = false
}

function onWindowResize() {
  if (userWidthPx.value != null) {
    userWidthPx.value = clampWidth(userWidthPx.value)
  }
}

onMounted(() => {
  window.addEventListener('resize', onWindowResize, { passive: true })
})

onUnmounted(() => {
  window.removeEventListener('resize', onWindowResize)
  cleanupResize()
})

const handleConfirm = () => emit('confirm')
const handleCancel = () => {
  emit('cancel')
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
/* ---------- Header ---------- */
.setting-drawer__header {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  padding: 2px 0;
}

.setting-drawer__header-icon {
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
  transition: background 0.2s ease;
}

.setting-drawer__header-text {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.setting-drawer__title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.setting-drawer__subtitle {
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
}

/* ---------- Body ---------- */
.setting-drawer__body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  /* The Body is the entry-animation host. Children (.form-item) get
     a subtle staggered slide-in to echo the model-card hover transform. */
  animation: setting-drawer-body-in 0.28s ease both;
}

@keyframes setting-drawer-body-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* ---------- Sections (consumed by ModelEditorDialog & friends) ---------- */
.setting-drawer__body :deep(.setting-drawer__section) {
  padding: 12px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 14px;
  animation: setting-drawer-section-in 0.32s ease both;

  &:first-child {
    padding-top: 0;
    animation-delay: 0.04s;
  }

  &:nth-child(2) {
    animation-delay: 0.08s;
  }

  &:nth-child(3) {
    animation-delay: 0.12s;
  }

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
}

@keyframes setting-drawer-section-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.setting-drawer__body :deep(.setting-drawer__section-title) {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
  user-select: none;
  display: flex;
  align-items: center;
  gap: 8px;

  /* A subtle leading bar — replaces the previous all-caps + letter-spacing
     trick (which mangles Chinese). Gives the section title a consistent
     visual anchor without yelling at the user. */
  &::before {
    content: '';
    width: 3px;
    height: 14px;
    background: var(--td-brand-color);
    border-radius: 2px;
  }
}

/* ---------- Footer ---------- */
.setting-drawer__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}

.setting-drawer__footer-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.setting-drawer__footer-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
</style>

<!--
  Non-scoped block: t-drawer renders header/footer wrappers outside the
  scoped style boundary in some TDesign builds, so we tweak chrome (border,
  padding) at the global level — namespaced under `.setting-drawer` to avoid
  bleeding into other drawers in the app.
-->
<style lang="less">
.setting-drawer {
  .t-drawer__header {
    padding: 14px 18px;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .t-drawer__body {
    padding: 16px 18px;
  }

  .t-drawer__footer {
    padding: 10px 18px;
    border-top: 1px solid var(--td-component-stroke);
    box-shadow: 0 -2px 8px rgba(15, 23, 42, 0.04);
  }
}

/* Visible resize handle — teleported to body, aligned with drawer left edge. */
.setting-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  cursor: col-resize;
  z-index: 2501;
  display: flex;
  align-items: center;
  justify-content: center;
  /* The handle is teleported separately from TDesign's sliding panel. Move it
     along the same path instead of letting it flash at the panel's final left
     edge while the drawer is still entering from the right. */
  animation: setting-drawer-resize-handle-in 0.28s cubic-bezier(0.38, 0, 0.24, 1) both;
}

@keyframes setting-drawer-resize-handle-in {
  from {
    transform: translateX(var(--setting-drawer-travel));
  }

  to {
    transform: translateX(0);
  }
}

.setting-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.setting-drawer-resize-handle:hover .setting-drawer-resize-line,
.setting-drawer-resize-handle--active .setting-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.setting-drawer--resizing .t-drawer__content {
  transition: none !important;
}
</style>
