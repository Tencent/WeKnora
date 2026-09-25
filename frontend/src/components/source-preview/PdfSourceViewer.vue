<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SourceLocateRequest, SourceLocator } from '@/utils/sourceLocator'
import { findInText } from '@/utils/sourceLocator'
import { findTextRange } from '@/utils/sourceLocatorDom'
import { loadPdfJs, type PdfJs } from './pdfjsLoader'

type PdfDocument = Awaited<ReturnType<PdfJs['getDocument']>['promise']>
type PdfPage = Awaited<ReturnType<PdfDocument['getPage']>>

/** A highlight drawn on a page, in fractions of the page box. */
type Mark = { left: number; top: number; width: number; height: number; kind: 'box' | 'text' | 'page' }

export type PdfLocateResult = { found: boolean; precise: boolean; page?: number }

const props = defineProps<{
  data: ArrayBuffer | null
  fileName?: string
  locate?: SourceLocateRequest | null
}>()

const emit = defineEmits<{
  located: [result: PdfLocateResult]
  error: [message: string]
}>()

const { t } = useI18n()

const scroller = ref<HTMLElement | null>(null)
const pageCount = ref(0)
const currentPage = ref(1)
const zoom = ref(1)
const loading = ref(false)
const error = ref('')
/** Height / width of each page; unknown pages borrow the first page's. */
const ratios = reactive<Record<number, number>>({})
const marks = reactive<Record<number, Mark[]>>({})
const pdf = shallowRef<PdfDocument | null>(null)

const pageElements = new Map<number, HTMLElement>()
type RenderEntry = {
  scale: number
  task?: { cancel: () => void }
  textLayer?: { cancel: () => void }
  /** Settles once the canvas and text layer are in place (or abandoned). */
  done?: Promise<void>
}
const rendered = new Map<number, RenderEntry>()
const renderOrder: number[] = []
const pageTexts = new Map<number, string>()
let pdfjs: PdfJs | null = null
let loadingTask: { destroy: () => Promise<void> } | null = null
let observer: IntersectionObserver | null = null
let resizeObserver: ResizeObserver | null = null
let containerWidth = 0
let disposed = false
let locateVersion = 0

const MAX_RENDERED_PAGES = 12
const PAGE_GAP = 12

function defaultRatio(): number {
  return ratios[1] || 1.414
}

function pageRatio(page: number): number {
  return ratios[page] || defaultRatio()
}

function pageStyle(page: number) {
  return { aspectRatio: `1 / ${pageRatio(page)}`, width: `${zoom.value * 100}%` }
}

function setPageRef(page: number, el: unknown) {
  const node = el as HTMLElement | null
  const previous = pageElements.get(page)
  if (previous && previous !== node) observer?.unobserve(previous)
  if (node) {
    pageElements.set(page, node)
    observer?.observe(node)
  } else {
    pageElements.delete(page)
  }
}

async function openDocument(data: ArrayBuffer) {
  const lib = await loadPdfJs()
  pdfjs = lib
  if (disposed) return
  // pdf.js takes ownership of the buffer; keep the caller's copy intact.
  const task = lib.getDocument({ data: new Uint8Array(data.slice(0)) })
  loadingTask = task
  const doc = await task.promise
  if (disposed) {
    await task.destroy()
    return
  }
  pdf.value = doc
  pageCount.value = doc.numPages
  const first = await doc.getPage(1)
  const vp = first.getViewport({ scale: 1 })
  ratios[1] = vp.height / vp.width
}

function renderScale(page: PdfPage): number {
  const vp = page.getViewport({ scale: 1 })
  const width = Math.max(200, containerWidth * zoom.value)
  return width / vp.width
}

async function renderPage(pageNumber: number): Promise<void> {
  const doc = pdf.value
  const el = pageElements.get(pageNumber)
  if (!doc || !el || !pdfjs) return
  const page = await doc.getPage(pageNumber)
  const scale = renderScale(page)
  const existing = rendered.get(pageNumber)
  if (existing && Math.abs(existing.scale - scale) < 0.01) {
    // Wait for a render still in flight, so callers find its text layer.
    await existing.done
    return
  }
  existing?.task?.cancel()
  existing?.textLayer?.cancel()

  const viewport = page.getViewport({ scale })
  ratios[pageNumber] = viewport.height / viewport.width

  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  const canvas = document.createElement('canvas')
  canvas.width = Math.floor(viewport.width * dpr)
  canvas.height = Math.floor(viewport.height * dpr)
  canvas.className = 'pdf-source-page__canvas'

  const entry: RenderEntry = { scale }
  rendered.set(pageNumber, entry)
  touchRendered(pageNumber)
  entry.done = paintPage(pageNumber, page, el, canvas, viewport, dpr, entry)
  await entry.done
}

async function paintPage(
  pageNumber: number,
  page: PdfPage,
  el: HTMLElement,
  canvas: HTMLCanvasElement,
  viewport: ReturnType<PdfPage['getViewport']>,
  dpr: number,
  entry: RenderEntry,
): Promise<void> {
  const lib = pdfjs
  if (!lib) return
  const task = page.render({
    canvas,
    viewport,
    transform: dpr !== 1 ? [dpr, 0, 0, dpr, 0, 0] : undefined,
  })
  entry.task = task
  try {
    await task.promise
  } catch {
    return // cancelled by a newer render or unrender
  }
  if (rendered.get(pageNumber) !== entry) return

  const textDiv = document.createElement('div')
  textDiv.className = 'textLayer'
  el.style.setProperty('--scale-factor', String(entry.scale))
  el.style.setProperty('--total-scale-factor', String(entry.scale))
  const oldCanvas = el.querySelector('.pdf-source-page__canvas')
  const oldText = el.querySelector('.textLayer')
  oldCanvas?.replaceWith(canvas)
  if (!oldCanvas) el.prepend(canvas)
  oldText?.remove()
  canvas.after(textDiv)

  const textLayer = new lib.TextLayer({
    textContentSource: page.streamTextContent(),
    container: textDiv,
    viewport,
  })
  entry.textLayer = textLayer
  try {
    await textLayer.render()
  } catch {
    // Text layers are best-effort; the page is still readable.
  }
}

function touchRendered(pageNumber: number) {
  const i = renderOrder.indexOf(pageNumber)
  if (i >= 0) renderOrder.splice(i, 1)
  renderOrder.push(pageNumber)
  while (renderOrder.length > MAX_RENDERED_PAGES) {
    const evict = renderOrder.shift()!
    unrenderPage(evict)
  }
}

function unrenderPage(pageNumber: number) {
  const entry = rendered.get(pageNumber)
  entry?.task?.cancel()
  entry?.textLayer?.cancel()
  rendered.delete(pageNumber)
  const el = pageElements.get(pageNumber)
  el?.querySelector('.pdf-source-page__canvas')?.remove()
  el?.querySelector('.textLayer')?.remove()
}

function onIntersect(entries: IntersectionObserverEntry[]) {
  for (const entry of entries) {
    if (!entry.isIntersecting) continue
    const page = Number((entry.target as HTMLElement).dataset.page)
    if (page) void renderPage(page)
  }
}

function onScroll() {
  const box = scroller.value
  if (!box) return
  const mid = box.scrollTop + box.clientHeight / 3
  for (const [page, el] of pageElements) {
    if (el.offsetTop <= mid && el.offsetTop + el.offsetHeight + PAGE_GAP > mid) {
      currentPage.value = page
      break
    }
  }
}

function rerenderVisible() {
  const pages = [...rendered.keys()]
  for (const p of pages) void renderPage(p)
}

function setZoom(next: number) {
  const box = scroller.value
  const anchor = box ? box.scrollTop / Math.max(1, box.scrollHeight) : 0
  zoom.value = Math.max(0.5, Math.min(3, Math.round(next * 10) / 10))
  void nextTick(() => {
    if (box) box.scrollTop = anchor * box.scrollHeight
    rerenderVisible()
  })
}

// ---------------------------------------------------------------------------
// Locating

function clearMarks() {
  for (const key of Object.keys(marks)) delete marks[Number(key)]
}

function addMark(page: number, mark: Mark) {
  ;(marks[page] ||= []).push(mark)
}

function scrollToPageFraction(page: number, fraction: number) {
  const box = scroller.value
  const el = pageElements.get(page)
  if (!box || !el) return
  // Instant, like scrollRectIntoContainer: nothing can cancel it mid-way.
  box.scrollTop = Math.max(0, el.offsetTop + el.offsetHeight * fraction - box.clientHeight / 3)
}

async function pageText(pageNumber: number): Promise<string> {
  const cached = pageTexts.get(pageNumber)
  if (cached !== undefined) return cached
  const doc = pdf.value
  if (!doc) return ''
  const page = await doc.getPage(pageNumber)
  const content = await page.getTextContent()
  const text = content.items.map((item) => ('str' in item ? item.str + (item.hasEOL ? '\n' : ' ') : '')).join('')
  pageTexts.set(pageNumber, text)
  return text
}

/** Draw the match for one of `quotes` on `pageNumber`; returns the top fraction or null. */
async function markQuoteOnPage(pageNumber: number, quotes: string[], version: number): Promise<number | null> {
  const text = await pageText(pageNumber)
  const quote = quotes.find((q) => findInText(text, q))
  if (!quote || version !== locateVersion) return null
  await renderPage(pageNumber)
  // A concurrent render may have replaced the one awaited; wait for the live one.
  await rendered.get(pageNumber)?.done
  if (version !== locateVersion) return null
  const el = pageElements.get(pageNumber)
  const layer = el?.querySelector('.textLayer')
  if (!el || !layer) return null
  const range = findTextRange(layer, quote)
  if (!range) return null
  const pageBox = el.getBoundingClientRect()
  const lines = mergeLineRects([...range.getClientRects()])
  let top: number | null = null
  for (const r of lines) {
    const mark: Mark = {
      kind: 'text',
      left: (r.left - pageBox.left) / pageBox.width,
      top: (r.top - pageBox.top) / pageBox.height,
      width: r.width / pageBox.width,
      height: r.height / pageBox.height,
    }
    if (mark.width <= 0 || mark.height <= 0) continue
    addMark(pageNumber, mark)
    top = top === null ? mark.top : Math.min(top, mark.top)
  }
  return top
}

/** Merge the per-glyph-run rects of a range into one rect per visual line. */
function mergeLineRects(rects: DOMRect[]): Array<{ left: number; top: number; width: number; height: number }> {
  const lines: Array<{ left: number; top: number; right: number; bottom: number }> = []
  for (const r of rects) {
    if (r.width < 0.5 || r.height < 0.5) continue
    const line = lines.find((l) => Math.min(l.bottom, r.bottom) - Math.max(l.top, r.top) > Math.min(r.height, l.bottom - l.top) * 0.5)
    if (line) {
      line.left = Math.min(line.left, r.left)
      line.right = Math.max(line.right, r.right)
      line.top = Math.min(line.top, r.top)
      line.bottom = Math.max(line.bottom, r.bottom)
    } else {
      lines.push({ left: r.left, top: r.top, right: r.right, bottom: r.bottom })
    }
  }
  return lines.map((l) => ({ left: l.left - 2, top: l.top - 1, width: l.right - l.left + 4, height: l.bottom - l.top + 2 }))
}

async function applyLocate(request: SourceLocateRequest | null | undefined) {
  const version = ++locateVersion
  clearMarks()
  if (!request || !pdf.value) return
  const total = pageCount.value
  const pdfLocators = request.locators.filter((l): l is SourceLocator & { page: number } =>
    l.type === 'pdf' && !!l.page && l.page >= 1 && l.page <= total)

  let firstPage = 0
  let firstTop = 0
  let precise = false
  const note = (page: number, top: number) => {
    if (!firstPage) {
      firstPage = page
      firstTop = top
    }
  }

  // Regions reported by the parser are drawn as they are.
  for (const loc of pdfLocators) {
    if (loc.bbox && loc.bbox.length === 4) {
      const [x0, y0, x1, y1] = loc.bbox
      addMark(loc.page, { kind: 'box', left: x0, top: y0, width: x1 - x0, height: y1 - y0 })
      note(loc.page, y0)
      precise = true
    }
  }

  // Pages without a region: find the quoted text on the page.
  if (!precise) {
    const pages = [...new Set(pdfLocators.map((l) => l.page))]
    for (const page of pages) {
      const quotes = [
        ...pdfLocators.filter((l) => l.page === page && l.quote).map((l) => l.quote as string),
        ...request.quotes,
      ]
      const top = quotes.length ? await markQuoteOnPage(page, quotes, version) : null
      if (version !== locateVersion) return
      if (top !== null) {
        note(page, top)
        precise = true
      } else {
        // Scanned pages have no text: outline the page itself.
        addMark(page, { kind: 'page', left: 0, top: 0, width: 1, height: 1 })
        note(page, 0)
      }
    }
  }

  // No page at all: search the document for the quote.
  if (!firstPage && request.quotes.length) {
    const limit = Math.min(total, 500)
    for (let page = 1; page <= limit; page++) {
      const text = await pageText(page)
      if (version !== locateVersion) return
      if (request.quotes.some((q) => findInText(text, q))) {
        const top = await markQuoteOnPage(page, request.quotes, version)
        if (version !== locateVersion) return
        if (top !== null) {
          note(page, top)
          precise = true
        }
        break
      }
    }
  }

  await nextTick()
  if (version !== locateVersion) return
  if (firstPage) {
    scrollToPageFraction(firstPage, firstTop)
    currentPage.value = firstPage
  }
  emit('located', { found: !!firstPage, precise, page: firstPage || undefined })
}

watch(
  () => props.locate?.token,
  () => {
    if (pdf.value) void applyLocate(props.locate)
  },
)

async function load() {
  error.value = ''
  if (!props.data) return
  loading.value = true
  try {
    await openDocument(props.data)
    await nextTick()
    containerWidth = scroller.value?.clientWidth ? scroller.value.clientWidth - 24 : 600
    if (props.locate) await applyLocate(props.locate)
  } catch (err) {
    console.error('PDF preview failed:', err)
    error.value = t('preview.loadFailed')
    emit('error', error.value)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  observer = new IntersectionObserver(onIntersect, { root: scroller.value, rootMargin: '600px 0px' })
  for (const el of pageElements.values()) observer.observe(el)
  resizeObserver = new ResizeObserver(() => {
    const width = scroller.value?.clientWidth ? scroller.value.clientWidth - 24 : 0
    if (width && Math.abs(width - containerWidth) > 8) {
      containerWidth = width
      rerenderVisible()
    }
  })
  if (scroller.value) resizeObserver.observe(scroller.value)
  void load()
})

onBeforeUnmount(() => {
  disposed = true
  locateVersion++
  observer?.disconnect()
  resizeObserver?.disconnect()
  for (const p of [...rendered.keys()]) unrenderPage(p)
  // Destroying the loading task also destroys the document and its worker.
  void loadingTask?.destroy()
})

const pageLabel = computed(() => (pageCount.value ? `${currentPage.value} / ${pageCount.value}` : ''))

defineExpose({ relocate: () => applyLocate(props.locate) })
</script>

<template>
  <div class="pdf-source-viewer">
    <div v-if="error" class="pdf-source-viewer__error">{{ error }}</div>
    <div
      ref="scroller"
      class="pdf-source-viewer__scroller"
      tabindex="0"
      :aria-label="fileName"
      @scroll.passive="onScroll"
    >
      <div v-if="loading && !pageCount" class="pdf-source-viewer__loading">
        <t-loading size="small" />
      </div>
      <div
        v-for="page in pageCount"
        :key="page"
        :ref="(el) => setPageRef(page, el)"
        class="pdf-source-page"
        :data-page="page"
        :style="pageStyle(page)"
      >
        <div class="pdf-source-page__marks" aria-hidden="true">
          <div
            v-for="(mark, i) in marks[page] || []"
            :key="i"
            class="pdf-source-mark"
            :class="`pdf-source-mark--${mark.kind}`"
            :style="{
              left: `${mark.left * 100}%`,
              top: `${mark.top * 100}%`,
              width: `${mark.width * 100}%`,
              height: `${mark.height * 100}%`,
            }"
          />
        </div>
      </div>
    </div>
    <div v-if="pageCount" class="pdf-source-viewer__bar">
      <span class="pdf-source-viewer__page">{{ pageLabel }}</span>
      <t-button
        theme="default" variant="text" size="small" shape="square"
        :aria-label="t('preview.zoomOut')" :title="t('preview.zoomOut')"
        @click="setZoom(zoom - 0.2)"
      >
        <template #icon><t-icon name="zoom-out" /></template>
      </t-button>
      <span class="pdf-source-viewer__zoom">{{ Math.round(zoom * 100) }}%</span>
      <t-button
        theme="default" variant="text" size="small" shape="square"
        :aria-label="t('preview.zoomIn')" :title="t('preview.zoomIn')"
        @click="setZoom(zoom + 0.2)"
      >
        <template #icon><t-icon name="zoom-in" /></template>
      </t-button>
    </div>
  </div>
</template>

<style scoped lang="less">
.pdf-source-viewer {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--td-bg-color-secondarycontainer);
}

.pdf-source-viewer__scroller {
  // Positioned so page offsetTop is measured from the scroll content.
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px;
  outline: none;
}

.pdf-source-viewer__loading,
.pdf-source-viewer__error {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

.pdf-source-page {
  position: relative;
  // Block flow with auto margins keeps zoomed pages scrollable to the left.
  margin: 0 auto 12px;
  max-width: none;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.12);
  // Text layers size themselves in these units.
  --scale-round-x: 1px;
  --scale-round-y: 1px;

  :deep(.pdf-source-page__canvas) {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
  }
}

.pdf-source-page__marks {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
}

.pdf-source-mark {
  position: absolute;
  border-radius: var(--app-radius-xs);
  background: color-mix(in srgb, var(--app-source-highlight) 55%, transparent);
  mix-blend-mode: multiply;

  &--box {
    outline: 1px solid var(--app-source-highlight);
  }

  &--page {
    background: transparent;
    outline: 2px solid var(--app-source-highlight);
    mix-blend-mode: normal;
  }
}

.pdf-source-viewer__bar {
  position: absolute;
  right: 16px;
  bottom: 12px;
  z-index: 3;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px 6px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.pdf-source-viewer__page {
  padding: 0 6px;
  font-variant-numeric: tabular-nums;
}

.pdf-source-viewer__zoom {
  min-width: 40px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
</style>

<style lang="less">
/* Minimal pdf.js text layer rules (from pdfjs-dist/web/pdf_viewer.css), so
   text is selectable and searchable without the full viewer stylesheet. */
.pdf-source-page .textLayer {
  position: absolute;
  text-align: initial;
  inset: 0;
  overflow: clip;
  opacity: 1;
  line-height: 1;
  letter-spacing: normal;
  word-spacing: normal;
  text-size-adjust: none;
  forced-color-adjust: none;
  transform-origin: 0 0;
  z-index: 1;
  --min-font-size: 1;
  --text-scale-factor: calc(var(--total-scale-factor) * var(--min-font-size));
  --min-font-size-inv: calc(1 / var(--min-font-size));

  :is(span, br) {
    color: transparent;
    position: absolute;
    white-space: pre;
    cursor: text;
    transform-origin: 0% 0%;
  }

  > :not(.markedContent),
  .markedContent span:not(.markedContent) {
    z-index: 1;
    --font-height: 0;
    font-size: calc(var(--text-scale-factor) * var(--font-height));
    --scale-x: 1;
    --rotate: 0deg;
    transform: rotate(var(--rotate)) scaleX(var(--scale-x)) scale(var(--min-font-size-inv));
  }

  .markedContent {
    display: contents;
  }

  ::selection {
    background: color-mix(in srgb, AccentColor, transparent 50%);
    color: transparent;
  }

  &[data-main-rotation="90"] { transform: rotate(90deg) translateY(-100%); }
  &[data-main-rotation="180"] { transform: rotate(180deg) translate(-100%, -100%); }
  &[data-main-rotation="270"] { transform: rotate(270deg) translateX(-100%); }
}
</style>
