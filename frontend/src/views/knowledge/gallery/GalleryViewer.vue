<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ImageAsset } from '@/api/image-gallery'
import { copyWithToast } from '@/utils/clipboard'

export interface GalleryAttrRow {
  key: string
  label: string
  value: string
}

const props = defineProps<{
  /** The images of the page on screen; the viewer walks them in order. */
  items: ImageAsset[]
  index: number
  /** Position of items[0] in the whole result set, so the counter and the
   *  prev/next bounds speak about every image, not just this page. */
  offset: number
  total: number
  /** A neighbouring page is being fetched; navigation waits for it. */
  loading: boolean
  thumbUrls: Record<string, string>
  resolveSrc: (url: string) => Promise<string>
  attrRows: (img: ImageAsset) => GalleryAttrRow[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'select', index: number): void
  /** Step past the page edge; the gallery loads the neighbouring page. */
  (e: 'step', delta: 1 | -1): void
  (e: 'open-source', knowledgeId: string): void
}>()

const NS = 'knowledgeEditor.wikiBrowser.gallery'
const { t, locale } = useI18n()

const current = computed<ImageAsset | null>(() => props.items[props.index] ?? null)
const position = computed(() => props.offset + props.index + 1)
const hasPrev = computed(() => position.value > 1)
const hasNext = computed(() => position.value < props.total)

function prev(): void {
  if (props.loading || !hasPrev.value) return
  if (props.index > 0) emit('select', props.index - 1)
  else emit('step', -1)
}

function next(): void {
  if (props.loading || !hasNext.value) return
  if (props.index < props.items.length - 1) emit('select', props.index + 1)
  else emit('step', 1)
}

// ---------------------------------------------------------------------------
// Zoom, pan and rotation. Scale 1 is "fit to the stage"; the translation is
// kept relative to the stage centre so zooming can anchor on the cursor.
// ---------------------------------------------------------------------------
const MIN_SCALE = 0.2
const MAX_SCALE = 8
const scale = ref(1)
const rotation = ref(0)
const tx = ref(0)
const ty = ref(0)
const dragging = ref(false)

function resetView(): void {
  scale.value = 1
  rotation.value = 0
  tx.value = 0
  ty.value = 0
}

function zoomTo(target: number, anchor?: { x: number; y: number }): void {
  const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, target))
  const k = next / scale.value
  const ax = anchor?.x ?? 0
  const ay = anchor?.y ?? 0
  tx.value = ax - (ax - tx.value) * k
  ty.value = ay - (ay - ty.value) * k
  scale.value = next
  // Nothing to pan once the image fits again; re-centre it.
  if (next <= fitScale()) {
    tx.value = 0
    ty.value = 0
  }
}

/** The scale at which the (possibly rotated) image fits the stage. */
function fitScale(): number {
  const img = imgEl.value
  const stage = stageEl.value
  if (!img || !stage || rotation.value % 180 === 0) return 1
  const w = img.clientWidth
  const h = img.clientHeight
  if (!w || !h) return 1
  const style = getComputedStyle(stage)
  const boxW = stage.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  const boxH = stage.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)
  return Math.min(1, boxW / h, boxH / w)
}

/** The scale at which one image pixel is one screen pixel. */
function actualScale(): number {
  const img = imgEl.value
  if (!img || !natural.value || !img.clientWidth) return 1
  return natural.value.w / img.clientWidth
}

function anchorOf(e: { clientX: number; clientY: number }): { x: number; y: number } {
  const rect = stageEl.value?.getBoundingClientRect()
  if (!rect) return { x: 0, y: 0 }
  return { x: e.clientX - rect.left - rect.width / 2, y: e.clientY - rect.top - rect.height / 2 }
}

const zoomIn = () => zoomTo(scale.value * 1.25)
const zoomOut = () => zoomTo(scale.value / 1.25)
const zoomActual = () => zoomTo(actualScale())

function rotate(): void {
  rotation.value = (rotation.value + 90) % 360
  tx.value = 0
  ty.value = 0
  scale.value = fitScale()
}

function onWheel(e: WheelEvent): void {
  if (!loaded.value) return
  zoomTo(scale.value * Math.exp(-e.deltaY * 0.002), anchorOf(e))
}

function onDblClick(e: MouseEvent): void {
  if (!loaded.value) return
  if (Math.abs(scale.value - fitScale()) > 0.01) {
    resetView()
    scale.value = fitScale()
  } else {
    zoomTo(Math.max(2, actualScale()), anchorOf(e))
  }
}

let dragStart: { x: number; y: number; tx: number; ty: number } | null = null

function onPointerDown(e: PointerEvent): void {
  if (e.button !== 0 || !loaded.value || scale.value <= fitScale()) return
  dragStart = { x: e.clientX, y: e.clientY, tx: tx.value, ty: ty.value }
  dragging.value = true
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onPointerMove(e: PointerEvent): void {
  if (!dragStart) return
  tx.value = dragStart.tx + e.clientX - dragStart.x
  ty.value = dragStart.ty + e.clientY - dragStart.y
}

function onPointerUp(): void {
  dragStart = null
  dragging.value = false
}

// ---------------------------------------------------------------------------
// Source resolution. The <img> is rendered only once a displayable source is
// in hand: a raw storage handle would fire the error handler and mask the
// image that is still being fetched. The token keeps a slow fetch from
// overwriting a faster one that navigated past it.
// ---------------------------------------------------------------------------
const src = ref('')
const loaded = ref(false)
const failed = ref(false)
const natural = ref<{ w: number; h: number } | null>(null)
let srcToken = 0

watch(
  () => current.value?.url,
  async (url) => {
    const token = ++srcToken
    resetView()
    src.value = ''
    loaded.value = false
    failed.value = false
    natural.value = null
    if (!url) return
    const resolved = await props.resolveSrc(url)
    if (token !== srcToken) return
    src.value = resolved
  },
  { immediate: true },
)

const imgEl = ref<HTMLImageElement | null>(null)
const stageEl = ref<HTMLElement | null>(null)

function onImgLoad(): void {
  loaded.value = true
  const el = imgEl.value
  if (el) natural.value = { w: el.naturalWidth, h: el.naturalHeight }
}

function onImgError(): void {
  failed.value = true
}

const canPan = computed(() => loaded.value && scale.value > 1)
const zoomPercent = computed(() => `${Math.round(scale.value * 100)}%`)
const imgStyle = computed(() => ({
  transform: `translate(${tx.value}px, ${ty.value}px) rotate(${rotation.value}deg) scale(${scale.value})`,
}))

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------
const EXT_BY_TYPE: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/gif': 'gif',
  'image/webp': 'webp',
  'image/svg+xml': 'svg',
  'image/bmp': 'bmp',
}

function downloadName(img: ImageAsset, type: string): string {
  const base = (img.source_name || 'image').replace(/\.[^./\\]+$/, '').replace(/[\\/:*?"<>|]+/g, '_')
  const fromUrl = /\.([a-z0-9]{2,5})(?:$|[?#])/i.exec(img.url)?.[1]
  const ext = EXT_BY_TYPE[type] || fromUrl || 'png'
  return `${base}-${position.value}.${ext}`
}

async function download(): Promise<void> {
  const img = current.value
  if (!img || !src.value) return
  try {
    const blob = await (await fetch(src.value)).blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = downloadName(img, blob.type)
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch {
    // A cross-origin image the browser may not read back: let it open instead.
    openOriginal()
  }
}

function openOriginal(): void {
  if (src.value) window.open(src.value, '_blank', 'noopener')
}

const copyText = (text: string) => copyWithToast(text, 'common.copySuccess')

// The info panel is a per-viewer preference; storage may be unavailable.
const INFO_KEY = 'weknora_gallery_viewer_info'
function readInfoPref(): boolean {
  try {
    return localStorage.getItem(INFO_KEY) !== '0'
  } catch {
    return true
  }
}
const showInfo = ref(readInfoPref())
function toggleInfo(): void {
  showInfo.value = !showInfo.value
  try {
    localStorage.setItem(INFO_KEY, showInfo.value ? '1' : '0')
  } catch {
    // Not remembered this time; the toggle itself still works.
  }
}

const attrs = computed(() => (current.value ? props.attrRows(current.value) : []))

function formatTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(locale.value)
}

// ---------------------------------------------------------------------------
// Filmstrip: keep the current thumbnail in view as the viewer moves.
// ---------------------------------------------------------------------------
const stripEl = ref<HTMLElement | null>(null)
watch(
  () => [props.index, props.items] as const,
  async () => {
    await nextTick()
    const el = stripEl.value?.querySelector<HTMLElement>('.igv-strip__item.is-current')
    el?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' })
  },
  { immediate: true },
)

// ---------------------------------------------------------------------------
// Keyboard. Bound on window while the viewer is mounted: the overlay itself is
// never focused, so a listener on it would never hear a key.
// ---------------------------------------------------------------------------
function onKey(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return
  const target = e.target as HTMLElement | null
  if (target?.closest?.('input, textarea, select, [contenteditable="true"]')) return
  const actions: Record<string, () => void> = {
    Escape: () => emit('close'),
    ArrowLeft: prev,
    ArrowRight: next,
    '+': zoomIn,
    '=': zoomIn,
    '-': zoomOut,
    '0': () => resetView(),
    r: rotate,
    i: toggleInfo,
  }
  const action = actions[e.key]
  if (!action) return
  e.preventDefault()
  action()
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div class="igv" :class="{ 'has-info': showInfo }" role="dialog" aria-modal="true" :aria-label="current?.caption || t(`${NS}.title`)">
      <div class="igv-main">
        <header class="igv-topbar">
          <div class="igv-topbar__title">
            <span class="igv-counter">{{ position }} / {{ total }}</span>
            <span class="igv-name" :title="current?.source_name">{{ current?.source_name || current?.knowledge_id }}</span>
          </div>
          <div class="igv-tools">
            <t-tooltip :content="t(`${NS}.zoomOut`)">
              <button type="button" class="igv-tool" :disabled="!loaded" :aria-label="t(`${NS}.zoomOut`)" @click="zoomOut">
                <t-icon name="zoom-out" />
              </button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.zoomReset`)">
              <button type="button" class="igv-tool igv-tool--text" :disabled="!loaded" @click="resetView">{{ zoomPercent }}</button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.zoomIn`)">
              <button type="button" class="igv-tool" :disabled="!loaded" :aria-label="t(`${NS}.zoomIn`)" @click="zoomIn">
                <t-icon name="zoom-in" />
              </button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.actualSize`)">
              <button type="button" class="igv-tool igv-tool--text" :disabled="!loaded" @click="zoomActual">1:1</button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.rotate`)">
              <button type="button" class="igv-tool" :disabled="!loaded" :aria-label="t(`${NS}.rotate`)" @click="rotate">
                <t-icon name="rotation" />
              </button>
            </t-tooltip>
            <span class="igv-tools__sep" />
            <t-tooltip :content="t(`${NS}.download`)">
              <button type="button" class="igv-tool" :disabled="!src" :aria-label="t(`${NS}.download`)" @click="download">
                <t-icon name="download" />
              </button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.openOriginal`)">
              <button type="button" class="igv-tool" :disabled="!src" :aria-label="t(`${NS}.openOriginal`)" @click="openOriginal">
                <t-icon name="jump" />
              </button>
            </t-tooltip>
            <t-tooltip :content="t(`${NS}.toggleInfo`)">
              <button type="button" class="igv-tool" :class="{ 'is-on': showInfo }" :aria-pressed="showInfo" :aria-label="t(`${NS}.toggleInfo`)" @click="toggleInfo">
                <t-icon name="info-circle" />
              </button>
            </t-tooltip>
            <span class="igv-tools__sep" />
            <t-tooltip :content="t(`${NS}.viewerClose`)">
              <button type="button" class="igv-tool" :aria-label="t(`${NS}.viewerClose`)" @click="emit('close')">
                <t-icon name="close" />
              </button>
            </t-tooltip>
          </div>
        </header>

        <div
          ref="stageEl"
          class="igv-stage"
          :class="{ 'can-pan': canPan, 'is-dragging': dragging }"
          @wheel.prevent="onWheel"
          @dblclick="onDblClick"
          @pointerdown="onPointerDown"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
          @pointercancel="onPointerUp"
          @click.self="emit('close')"
        >
          <img
            v-if="src && !failed"
            ref="imgEl"
            :src="src"
            :alt="current?.caption"
            class="igv-image"
            :class="{ 'is-loaded': loaded }"
            :style="imgStyle"
            draggable="false"
            @load="onImgLoad"
            @error="onImgError"
          />
          <div v-if="failed" class="igv-state">
            <t-icon name="image-error" size="32px" />
            <span>{{ t(`${NS}.imageLoadError`) }}</span>
          </div>
          <t-loading v-else-if="!loaded || loading" class="igv-state" size="medium" />

          <button type="button" class="igv-nav igv-nav--prev" :disabled="!hasPrev || loading" :aria-label="t(`${NS}.prev`)" @click.stop="prev">
            <t-icon name="chevron-left" size="24px" />
          </button>
          <button type="button" class="igv-nav igv-nav--next" :disabled="!hasNext || loading" :aria-label="t(`${NS}.next`)" @click.stop="next">
            <t-icon name="chevron-right" size="24px" />
          </button>
        </div>

        <div v-if="items.length > 1" ref="stripEl" class="igv-strip">
          <button
            v-for="(img, i) in items"
            :key="img.id"
            type="button"
            class="igv-strip__item"
            :class="{ 'is-current': i === index }"
            :aria-label="img.caption || String(offset + i + 1)"
            @click="emit('select', i)"
          >
            <img v-if="thumbUrls[img.id]" :src="thumbUrls[img.id]" alt="" draggable="false" />
          </button>
        </div>
      </div>

      <aside v-if="showInfo && current" class="igv-info">
        <section class="igv-section">
          <div class="igv-section__head">
            <h3>{{ t(`${NS}.caption`) }}</h3>
            <button v-if="current.caption" type="button" class="igv-copy" @click="copyText(current.caption)">
              <t-icon name="file-copy" size="14px" />{{ t(`${NS}.copy`) }}
            </button>
          </div>
          <p class="igv-text" :class="{ 'is-muted': !current.caption }">{{ current.caption || t(`${NS}.noCaption`) }}</p>
        </section>

        <section class="igv-section">
          <div class="igv-section__head">
            <h3>{{ t(`${NS}.ocr`) }}</h3>
            <button v-if="current.ocr_text" type="button" class="igv-copy" @click="copyText(current.ocr_text)">
              <t-icon name="file-copy" size="14px" />{{ t(`${NS}.copy`) }}
            </button>
          </div>
          <p class="igv-text" :class="{ 'is-muted': !current.ocr_text }">{{ current.ocr_text || t(`${NS}.noOcr`) }}</p>
        </section>

        <section v-if="attrs.length" class="igv-section">
          <div class="igv-section__head"><h3>{{ t(`${NS}.attributes`) }}</h3></div>
          <dl class="igv-props">
            <template v-for="a in attrs" :key="a.key">
              <dt>{{ a.label }}</dt>
              <dd>{{ a.value }}</dd>
            </template>
          </dl>
        </section>

        <section class="igv-section">
          <div class="igv-section__head"><h3>{{ t(`${NS}.details`) }}</h3></div>
          <dl class="igv-props">
            <dt>{{ t(`${NS}.source`) }}</dt>
            <dd>
              <button type="button" class="igv-source" :title="t(`${NS}.openSource`)" @click="emit('open-source', current.knowledge_id)">
                <t-icon name="file" size="14px" />
                <span>{{ current.source_name || current.knowledge_id }}</span>
              </button>
            </dd>
            <template v-if="natural">
              <dt>{{ t(`${NS}.dimensions`) }}</dt>
              <dd>{{ natural.w }} × {{ natural.h }}</dd>
            </template>
            <dt>{{ t(`${NS}.status`) }}</dt>
            <dd>
              <t-tag size="small" variant="light" :theme="current.is_enabled ? 'success' : 'default'">
                {{ t(`${NS}.attr.builtin_is_enabled_value_${current.is_enabled ? 'true' : 'false'}`) }}
              </t-tag>
            </dd>
            <template v-if="current.created_at">
              <dt>{{ t(`${NS}.attr.builtin_created_at`) }}</dt>
              <dd>{{ formatTime(current.created_at) }}</dd>
            </template>
          </dl>
        </section>
      </aside>
    </div>
  </Teleport>
</template>

<style scoped lang="less">
// The stage is always dark, whatever the app theme: images read best on a
// neutral dark ground, and the controls over it are sized for that.
.igv {
  --igv-stage-bg: #111214;
  --igv-chrome-fg: rgb(255 255 255 / 86%);
  --igv-chrome-muted: rgb(255 255 255 / 55%);
  --igv-chrome-hover: rgb(255 255 255 / 12%);

  position: fixed;
  inset: 0;
  z-index: var(--z-drawer);
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  background: var(--igv-stage-bg);
  color: var(--igv-chrome-fg);

  &.has-info {
    grid-template-columns: minmax(0, 1fr) 360px;
  }
}

.igv-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.igv-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  height: 52px;
  padding: 0 12px 0 20px;
  flex-shrink: 0;

  &__title {
    display: flex;
    align-items: baseline;
    gap: 12px;
    min-width: 0;
  }
}

.igv-counter {
  font-size: var(--app-text-md);
  font-variant-numeric: tabular-nums;
  color: var(--igv-chrome-muted);
  white-space: nowrap;
}

.igv-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-base);
}

.igv-tools {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;

  &__sep {
    width: 1px;
    height: 18px;
    margin: 0 6px;
    background: rgb(255 255 255 / 16%);
  }
}

.igv-tool {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  height: 32px;
  padding: 0 6px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--igv-chrome-fg);
  font: inherit;
  font-size: var(--app-text-xl);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;

  &--text {
    min-width: 48px;
    font-size: var(--app-text-sm);
    font-variant-numeric: tabular-nums;
  }

  &:hover:not(:disabled),
  &.is-on {
    background: var(--igv-chrome-hover);
  }

  &:disabled {
    opacity: 0.35;
    cursor: default;
  }

  &:focus-visible {
    outline: 2px solid var(--app-focus-border);
    outline-offset: 1px;
  }
}

.igv-stage {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  padding: 16px 72px;
  user-select: none;
  touch-action: none;

  &.can-pan { cursor: grab; }
  &.is-dragging { cursor: grabbing; }
}

.igv-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  opacity: 0;
  transition: transform var(--app-motion-fast) ease, opacity var(--app-motion-fast) ease;
  will-change: transform;

  &.is-loaded { opacity: 1; }

  .is-dragging & { transition: opacity var(--app-motion-fast) ease; }
}

.igv-state {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--igv-chrome-muted);
  font-size: var(--app-text-md);
  pointer-events: none;
}

.igv-nav {
  position: absolute;
  top: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  margin-top: -22px;
  border: 0;
  border-radius: var(--app-radius-pill);
  background: rgb(255 255 255 / 10%);
  color: var(--igv-chrome-fg);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease, opacity var(--app-motion-fast) ease;

  &--prev { left: 16px; }
  &--next { right: 16px; }

  &:hover:not(:disabled) { background: rgb(255 255 255 / 20%); }
  &:disabled { opacity: 0.2; cursor: default; }
  &:focus-visible { outline: 2px solid var(--app-focus-border); outline-offset: 2px; }
}

.igv-strip {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
  padding: 10px 20px 14px;
  overflow-x: auto;
  scrollbar-width: thin;
  scrollbar-color: rgb(255 255 255 / 20%) transparent;

  &__item {
    flex: 0 0 auto;
    width: 56px;
    height: 42px;
    padding: 0;
    border: 2px solid transparent;
    border-radius: var(--app-radius-xs);
    overflow: hidden;
    background: rgb(255 255 255 / 8%);
    opacity: 0.5;
    cursor: pointer;
    transition: opacity var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

    img {
      display: block;
      width: 100%;
      height: 100%;
      object-fit: cover;
    }

    &:hover { opacity: 0.85; }

    &.is-current {
      opacity: 1;
      border-color: var(--td-brand-color);
    }
  }
}

.igv-info {
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  border-left: 1px solid var(--td-component-stroke);
}

.igv-section {
  & + & {
    margin-top: 20px;
    padding-top: 16px;
    border-top: 1px solid var(--td-component-stroke);
  }

  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 8px;

    h3 {
      margin: 0;
      font-size: var(--app-text-md);
      font-weight: 600;
      color: var(--td-text-color-secondary);
    }
  }
}

.igv-copy {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container-hover);
  }
}

.igv-text {
  margin: 0;
  font-size: var(--app-text-base);
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;

  &.is-muted { color: var(--td-text-color-placeholder); }
}

.igv-props {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px 16px;
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 22px;

  dt { color: var(--td-text-color-secondary); }
  dd {
    margin: 0;
    min-width: 0;
    text-align: right;
    word-break: break-word;
  }
}

.igv-source {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  font: inherit;
  cursor: pointer;

  span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &:hover span { text-decoration: underline; }
}

@media (max-width: 900px) {
  .igv.has-info { grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr) 40%; }
  .igv-info { border-left: 0; border-top: 1px solid var(--td-component-stroke); }
  .igv-stage { padding: 12px 56px; }
}
</style>
