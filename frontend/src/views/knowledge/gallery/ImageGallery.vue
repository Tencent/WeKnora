<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  listGalleryImages,
  fetchGalleryConfig,
  type GalleryConfig,
  type GalleryResolvedAttr,
  type ImageAsset,
  type ImageListParams,
} from '@/api/image-gallery'
import { updateMyPreferences } from '@/api/auth'
import EmptyState from '@/components/EmptyState.vue'
import { galleryImageRequest } from './galleryImageSrc'
import GalleryViewer, { type GalleryAttrRow } from './GalleryViewer.vue'
import GallerySourcePicker from './GallerySourcePicker.vue'

const props = defineProps<{
  knowledgeBaseId: string
}>()

const emit = defineEmits<{
  (e: 'open-source-doc', knowledgeId: string): void
}>()

const NS = 'knowledgeEditor.wikiBrowser.gallery'
const { t, te } = useI18n()

// ---------------------------------------------------------------------------
// Data state
// ---------------------------------------------------------------------------
const loading = ref(false)
const error = ref('')
const items = ref<ImageAsset[]>([])
const total = ref(0)
const page = ref(0)
const pageSize = 24
const exhausted = ref(false)
const hasMore = computed(() => !exhausted.value && page.value * pageSize < total.value)

const keyword = ref('')
const sortBy = ref('')
const sortOrder = ref<'asc' | 'desc'>('desc')

const filterPanelVisible = ref(false)
const sortPanelVisible = ref(false)
const scrollEl = ref<HTMLElement | null>(null)
const gridEl = ref<HTMLElement | null>(null)
const loadMoreEl = ref<HTMLElement | null>(null)
const imageRatios = ref<Record<string, number>>({})
let cardObserver: ResizeObserver | undefined
let scrollObserver: ResizeObserver | undefined

// Source-document restriction (the "filter by document" picker). An empty
// array means every document of the KB; otherwise only images whose owning
// document is listed are fetched. Edited from the toolbar's source panel.
const sourceDocIds = ref<string[]>([])
const sourcePickerVisible = ref(false)

// Attribute selections for free-text ("keywords") attributes: namespaced attr
// id -> literal values (OR within the attribute, AND across attributes).
const attrSelections = ref<Record<string, string[]>>({})

// Per-value verdicts, namespaced attr id -> value -> "off" | "on". An absent
// key is the neutral position: an image carrying that value stays exactly as
// visible as it was, which is what makes the default state show everything.
const attrVerdicts = ref<Record<string, Record<string, string>>>({})

// ---------------------------------------------------------------------------
// Gallery contract (self-describing, fetched once per mount)
//
// Everything the UI offers — filter sections, searchable fields, sort
// options — comes from the contract. No gallery rule is hardcoded here, so
// new backend attributes and runtime policy edits light up on reload.
// ---------------------------------------------------------------------------
const configLoaded = ref(false)
const config = ref<GalleryConfig>({
  attribute_sources: [],
  attributes: [],
  mode: 'all',
  status: {},
})
const searchMode = ref<'all' | 'custom'>('all')
// Per-attribute search toggle, keyed by namespaced attr id ("on"/"off").
// Unrecorded fields are off in custom mode; source declarations carry no
// on/off — activation is always the user's own record.
const searchStatus = ref<Record<string, string>>({})

const filterAttrs = computed(() => config.value.attributes.filter((a) => a.usage.in_filter))
const searchAttrs = computed(() => config.value.attributes.filter((a) => a.usage.in_searchfield))
const sortAttrs = computed(() => config.value.attributes.filter((a) => a.usage.in_sortfield))

// The fields actually searched right now: every eligible field in "all"
// mode; the user's on-record in custom mode.
const activeSearchIds = computed(() => {
  if (searchMode.value === 'all') return searchAttrs.value.map((a) => a.id)
  return searchAttrs.value.filter((a) => searchStatus.value[a.id] === 'on').map((a) => a.id)
})

// ---------------------------------------------------------------------------
// Image URL resolution
//
// Backend returns storage handles (local://, minio://, resource://, ...) which
// the browser cannot render directly. They must be fetched through the KB file
// proxy (galleryImageRequest) and turned into object URLs first.
// Normal http(s) URLs pass through untouched.
// ---------------------------------------------------------------------------
const thumbUrls = ref<Record<string, string>>({})
const thumbBroken = ref<Record<string, boolean>>({})
// Object URLs keyed by the raw storage URL they were fetched for, so a
// thumbnail and the viewer share one blob and a reload reuses what is already
// on screen instead of fetching (and leaking) a fresh copy.
const blobByRawUrl = new Map<string, string>()
// Fetches in flight, so the grid and the viewer asking for the same image at
// once share one request.
const inflight = new Map<string, Promise<string>>()
let imageRequests = new AbortController()

function abortImageRequests(): void {
  imageRequests.abort()
  inflight.clear()
}

/** Revoke every cached object URL whose raw URL is not in keep. */
function releaseBlobs(keep: Set<string> = new Set()): void {
  for (const [raw, objectUrl] of blobByRawUrl) {
    if (keep.has(raw)) continue
    URL.revokeObjectURL(objectUrl)
    blobByRawUrl.delete(raw)
  }
}

async function fetchBlobUrl(rawUrl: string, signal: AbortSignal): Promise<string> {
  const req = galleryImageRequest(rawUrl, props.knowledgeBaseId)
  if (!req) return rawUrl
  try {
    const resp = await fetch(req.url, { headers: req.headers, signal })
    if (!resp.ok || signal.aborted) return rawUrl
    const blob = await resp.blob()
    // Cancellation can race with a completed body read. Never create a URL
    // after this query's cleanup, even if the response ignored the abort.
    if (signal.aborted) return rawUrl
    const objectUrl = URL.createObjectURL(blob)
    blobByRawUrl.set(rawUrl, objectUrl)
    return objectUrl
  } catch {
    return rawUrl
  }
}

function resolveImageSrc(rawUrl: string): Promise<string> {
  if (imageRequests.signal.aborted) return Promise.resolve(rawUrl)
  const cached = blobByRawUrl.get(rawUrl)
  if (cached) return Promise.resolve(cached)
  let pending = inflight.get(rawUrl)
  if (!pending) {
    pending = fetchBlobUrl(rawUrl, imageRequests.signal).finally(() => {
      // A newer query may already have started the same URL's replacement.
      if (inflight.get(rawUrl) === pending) inflight.delete(rawUrl)
    })
    inflight.set(rawUrl, pending)
  }
  return pending
}

async function resolveThumbnails(imgs: ImageAsset[], token: number) {
  await Promise.all(imgs.map(async (img) => {
    const url = await resolveImageSrc(img.url)
    if (token !== listToken) return
    thumbUrls.value[img.id] = url
  }))
  if (token === listToken) releaseBlobs(new Set(items.value.map((img) => img.url)))
}

function onThumbLoad(id: string, event: Event) {
  const image = event.target as HTMLImageElement
  if (image.naturalWidth && image.naturalHeight) {
    imageRatios.value[id] = image.naturalWidth / image.naturalHeight
  }
}

// Keep DOM order (and keyboard/viewer order) while cards span only the rows
// their natural image height needs. Existing cards stay put when appending.
function sizeCards(cards: HTMLElement[]) {
  const spans = cards.map((card) => Math.ceil((card.getBoundingClientRect().height + 12) / 13))
  cards.forEach((card, index) => {
    card.style.gridRowEnd = `span ${spans[index]}`
  })
}

function observeCards() {
  cardObserver?.disconnect()
  const cards = Array.from(gridEl.value?.querySelectorAll<HTMLElement>('.ig-card') ?? [])
  sizeCards(cards)
  cards.forEach((card) => cardObserver?.observe(card))
}

function checkLoadMore() {
  if (loading.value || error.value || viewerOpen.value || !hasMore.value) return
  const scroll = scrollEl.value
  const sentinel = loadMoreEl.value
  if (scroll && sentinel && scroll.clientHeight > 0 &&
      sentinel.getBoundingClientRect().top <= scroll.getBoundingClientRect().bottom + 300) {
    void loadMore()
  }
}

function onThumbError(id: string) {
  thumbBroken.value = { ...thumbBroken.value, [id]: true }
}

// ---------------------------------------------------------------------------
// Label helpers
//
// The contract carries the backend's default-language wording. Locale files
// overlay translations keyed by the (sanitized) attribute id; anything not
// translated falls back to the contract text.
//
// Every attribute and value carries two pieces of text on purpose: a short
// `label` for space-constrained controls (a filter checkbox, a sort option)
// and a `description` sentence for wherever there is room. The helpers below
// always return the short one and expose the long one separately, so a compact
// panel never has to stretch to a full sentence.
//
// A translation always wins: the contract's wording is only the fallback for
// attributes no locale knows yet. Asking the locale first matters, because a
// source carries its own default-language text and would otherwise shadow the
// translation for every locale but its own.
// ---------------------------------------------------------------------------
/** Where the gallery's own attribute wording lives in the locale files. */
const ATTR_NAMESPACE = 'knowledgeEditor.wikiBrowser.gallery.attr'

const sanitizeKey = (id: string) => id.replace(/[:.]/g, '_')

/** The pipeline's own wording for an attribute, keyed by its source-local name. */
function pipelineKey(attr: GalleryResolvedAttr, suffix = ''): string {
  return `imageAttr.${(attr.name || '').replace(/[.\s]/g, '_')}${suffix}`
}

function attrLabel(attr: GalleryResolvedAttr): string {
  const key = `${ATTR_NAMESPACE}.${sanitizeKey(attr.id)}`
  if (te(key)) return t(key)
  // Overlay the attribute pipeline's own translations when present.
  const legacy = pipelineKey(attr, '.label')
  if (te(legacy)) return t(legacy)
  return attr.label || attr.id
}

/** The sentence explaining an attribute, in words where the source gave one. */
function attrDescription(attr: GalleryResolvedAttr): string {
  const key = `${ATTR_NAMESPACE}.${sanitizeKey(attr.id)}_description`
  if (te(key)) return t(key)
  // Attributes contributed by an attribute pipeline ship no gallery-specific
  // text, so fall through to the pipeline's own translations.
  const legacy = pipelineKey(attr, '.description')
  if (te(legacy)) return t(legacy)
  return attr.description || ''
}

/**
 * The short display name of one allowed value. The locale wins over the
 * wording the source shipped; only a value no locale knows about falls back to
 * the source text, and one that spelled nothing out reads as the raw value.
 */
function attrValueLabel(attr: GalleryResolvedAttr, value: string): string {
  const key = `${ATTR_NAMESPACE}.${sanitizeKey(attr.id)}_value_${value}`
  if (te(key)) return t(key)
  const legacy = pipelineKey(attr, `.values.${value}.label`)
  if (te(legacy)) return t(legacy)
  return attr.values?.find((v) => v.value === value)?.label || value
}

/** The sentence explaining one allowed value; empty when the source has none. */
function attrValueDescription(attr: GalleryResolvedAttr, value: string): string {
  const key = `${ATTR_NAMESPACE}.${sanitizeKey(attr.id)}_value_${value}_description`
  if (te(key)) return t(key)
  const legacy = pipelineKey(attr, `.values.${value}.description`)
  if (te(legacy)) return t(legacy)
  return attr.values?.find((v) => v.value === value)?.description || ''
}

/** One observed attribute value of an image, in words where possible. */
function displayObservedValue(attr: GalleryResolvedAttr | undefined, raw: unknown): string {
  const normalized = typeof raw === 'boolean' ? String(raw) : String(raw ?? '')
  if (!attr) return normalized
  return attrValueLabel(attr, normalized)
}

function findAttrByRawName(name: string): GalleryResolvedAttr | undefined {
  return config.value.attributes.find((a) => a.name === name)
}

// ---------------------------------------------------------------------------
// Filters
//
// One attribute value takes one of three positions: neutral (leave those
// images alone), "off" (hide them), "on" (keep them even when another rule
// hides them). A value the user never touched is absent from the map rather
// than stored as a word, and never reaches the server.
// ---------------------------------------------------------------------------
const VERDICTS = ['default', 'off', 'on'] as const
type Verdict = (typeof VERDICTS)[number]

/** The verdicts that carry meaning, i.e. the ones worth putting on the wire. */
const activeRules = computed(() => {
  const out: Record<string, Record<string, string>> = {}
  for (const [id, perValue] of Object.entries(attrVerdicts.value)) {
    const clean: Record<string, string> = {}
    for (const [value, verdict] of Object.entries(perValue)) {
      if (verdict === 'off' || verdict === 'on') clean[value] = verdict
    }
    if (Object.keys(clean).length > 0) out[id] = clean
  }
  return out
})

const activeSelections = computed(() =>
  Object.fromEntries(Object.entries(attrSelections.value).filter(([, v]) => v.length)),
)

/** How many constraints narrow the list; the filter button shows it. */
const activeFilterCount = computed(() => {
  let n = Object.keys(activeSelections.value).length
  for (const perValue of Object.values(activeRules.value)) n += Object.keys(perValue).length
  if (searchMode.value === 'custom') n += 1
  return n
})

const isNarrowed = computed(
  () =>
    !!keyword.value.trim() ||
    Object.keys(activeRules.value).length > 0 ||
    Object.keys(activeSelections.value).length > 0 ||
    sourceDocIds.value.length > 0,
)

function verdictOf(attrId: string, value: string): Verdict {
  return (attrVerdicts.value[attrId]?.[value] as Verdict) || 'default'
}

function setVerdict(attrId: string, value: string, verdict: Verdict): void {
  if (verdictOf(attrId, value) === verdict) return
  const perValue = { ...(attrVerdicts.value[attrId] || {}) }
  if (verdict === 'default') delete perValue[value]
  else perValue[value] = verdict
  attrVerdicts.value = { ...attrVerdicts.value, [attrId]: perValue }
  reload()
}

function verdictLabel(v: Verdict): string {
  if (v === 'off') return t(`${NS}.verdictOff`)
  if (v === 'on') return t(`${NS}.verdictOn`)
  return t(`${NS}.verdictDefault`)
}

function onKeywordsInput(attrId: string, raw: string) {
  const values = raw
    .split(/[,，;；\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
  attrSelections.value = { ...attrSelections.value, [attrId]: values }
  reload()
}

function keywordsInputValue(attrId: string): string {
  return (attrSelections.value[attrId] || []).join(', ')
}

/** Drop every constraint in the filter panel, search scope included. */
function clearFilters(): void {
  attrVerdicts.value = {}
  attrSelections.value = {}
  if (searchMode.value === 'custom') {
    searchMode.value = 'all'
    persistSearchPrefs()
  }
  reload()
}

/** The empty state's way back: forget the keyword and source documents as well. */
function clearAll(): void {
  if (sourceTimer) clearTimeout(sourceTimer)
  keyword.value = ''
  sourceDocIds.value = []
  clearFilters()
}

/** Ticked in the source panel: restrict the listing to these documents. */
let sourceTimer: ReturnType<typeof setTimeout> | undefined
function onSourceDocsChange(ids: string[]): void {
  sourceDocIds.value = ids
  if (sourceTimer) clearTimeout(sourceTimer)
  sourceTimer = setTimeout(() => reload(), 300)
}

// ---------------------------------------------------------------------------
// Search scope (mode + per-field toggles, persisted as the user's own record)
//
// Ticking every field is the same as "all" mode, so the panel needs no
// separate switch: the mode follows the ticks. The last ticked field cannot
// be unticked — a search over no field would find nothing.
// ---------------------------------------------------------------------------
let prefsTimer: ReturnType<typeof setTimeout> | undefined
function persistSearchPrefs() {
  if (!configLoaded.value) return
  if (prefsTimer) clearTimeout(prefsTimer)
  prefsTimer = setTimeout(() => {
    void updateMyPreferences({
      gallery: { mode: searchMode.value, status: { ...searchStatus.value } },
    })
  }, 500)
}

function onSearchFieldToggle(attrId: string, checked: boolean) {
  const on = new Set(activeSearchIds.value)
  if (checked) on.add(attrId)
  else on.delete(attrId)
  const next: Record<string, string> = { ...searchStatus.value }
  for (const attr of searchAttrs.value) next[attr.id] = on.has(attr.id) ? 'on' : 'off'
  searchStatus.value = next
  searchMode.value = on.size === searchAttrs.value.length ? 'all' : 'custom'
  persistSearchPrefs()
  if (keyword.value.trim()) reload()
}

function isLastSearchField(attrId: string): boolean {
  const ids = activeSearchIds.value
  return ids.length === 1 && ids[0] === attrId
}

// ---------------------------------------------------------------------------
// Sort
// ---------------------------------------------------------------------------
const sortFieldLabel = computed(() => {
  const attr = sortAttrs.value.find((a) => a.id === sortBy.value)
  return attr ? attrLabel(attr) : ''
})

function selectSortField(id: string): void {
  if (sortBy.value === id) return
  sortBy.value = id
  reload()
}

function selectSortOrder(order: 'asc' | 'desc'): void {
  if (sortOrder.value === order) return
  sortOrder.value = order
  reload()
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------
function buildParams(): ImageListParams {
  const rules = activeRules.value
  const selections = activeSelections.value
  return {
    keyword: keyword.value.trim() || undefined,
    searchIn: activeSearchIds.value,
    sortBy: sortBy.value || undefined,
    sortOrder: sortOrder.value,
    attrFilters: Object.keys(selections).length ? selections : undefined,
    attrRules: Object.keys(rules).length ? rules : undefined,
    knowledgeIds: sourceDocIds.value.length ? sourceDocIds.value : undefined,
    pageSize,
  }
}

// A query owns all of its appended pages; stale responses cannot replace it.
let listToken = 0
let queryParams: ImageListParams = {}

async function fetchNextPage(): Promise<boolean> {
  if (!props.knowledgeBaseId) return false
  const token = listToken
  const nextPage = page.value + 1
  loading.value = true
  error.value = ''
  try {
    const res = await listGalleryImages(props.knowledgeBaseId, { ...queryParams, page: nextPage })
    if (token !== listToken) return false
    const existing = new Set(items.value.map((img) => img.id))
    const incoming = res.items.filter((img) => !existing.has(img.id))
    items.value.push(...incoming)
    page.value = nextPage
    total.value = res.total
    exhausted.value = res.items.length === 0
    void resolveThumbnails(incoming, token)
    await nextTick()
    observeCards()
    return true
  } catch (e) {
    if (token === listToken) error.value = e instanceof Error ? e.message : String(e)
    return false
  } finally {
    if (token === listToken) {
      loading.value = false
      void nextTick(checkLoadMore)
    }
  }
}

function loadMore(): Promise<boolean> {
  if (loading.value || !hasMore.value) return Promise.resolve(false)
  return fetchNextPage()
}

function resetResults() {
  listToken++
  abortImageRequests()
  imageRequests = new AbortController()
  viewerOpen.value = false
  items.value = []
  total.value = 0
  page.value = 0
  exhausted.value = false
  thumbUrls.value = {}
  thumbBroken.value = {}
  imageRatios.value = {}
  scrollEl.value?.scrollTo({ top: 0 })
}

function reload() {
  resetResults()
  queryParams = buildParams()
  return fetchNextPage()
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => reload(), 350)
}

function onSearchNow() {
  if (searchTimer) clearTimeout(searchTimer)
  reload()
}

// ---------------------------------------------------------------------------
// Viewer
// ---------------------------------------------------------------------------
const viewerOpen = ref(false)
const viewerIndex = ref(0)

function openViewer(index: number) {
  viewerIndex.value = index
  viewerOpen.value = true
}

function closeViewer() {
  if (!viewerOpen.value) return
  viewerOpen.value = false
  // The viewer may have walked onto another image; bring
  // the card it stopped on into view so the grid picks up where it left off.
  const index = viewerIndex.value
  void nextTick(() => {
    scrollEl.value
      ?.querySelectorAll<HTMLElement>('.ig-card')
      [index]?.scrollIntoView({ block: 'nearest' })
  })
}

/** At the loaded edge, append the next batch and keep the viewer continuous. */
async function onViewerLoadMore() {
  const token = listToken
  const nextIndex = items.value.length
  if (await loadMore() && token === listToken && viewerOpen.value && items.value.length > nextIndex) {
    viewerIndex.value = nextIndex
  }
}

function onOpenSource(knowledgeId: string) {
  closeViewer()
  emit('open-source-doc', knowledgeId)
}

function attrRows(img: ImageAsset): GalleryAttrRow[] {
  return Object.entries(img.attrs || {}).map(([name, raw]) => {
    const attr = findAttrByRawName(name)
    return {
      key: name,
      label: attr ? attrLabel(attr) : name,
      value: displayObservedValue(attr, raw),
    }
  })
}

function sourceLabel(img: ImageAsset): string {
  return img.source_name || img.knowledge_id
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------
onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
  if (sourceTimer) clearTimeout(sourceTimer)
  // Orphan any in-flight listing so it cannot map new blobs after unmount.
  listToken++
  initToken++
  abortImageRequests()
  cardObserver?.disconnect()
  scrollObserver?.disconnect()
  releaseBlobs()
})

// Newest first unless the contract no longer offers it; picking by id keeps
// the default independent of source registration order.
const DEFAULT_SORT_ID = 'builtin:created_at'

let initToken = 0

// Loads the contract and the first page for the current knowledge base. It
// also runs on a knowledge base switch: the component instance is reused, so
// everything scoped to the previous knowledge base is dropped first.
async function initGallery() {
  const token = ++initToken
  resetResults()
  if (searchTimer) clearTimeout(searchTimer)
  if (sourceTimer) clearTimeout(sourceTimer)
  error.value = ''
  keyword.value = ''
  sortBy.value = ''
  attrSelections.value = {}
  attrVerdicts.value = {}
  sourceDocIds.value = []
  sourcePickerVisible.value = false
  releaseBlobs()
  configLoaded.value = false
  config.value = { attribute_sources: [], attributes: [], mode: 'all', status: {} }
  loading.value = true
  try {
    const cfg = await fetchGalleryConfig(props.knowledgeBaseId)
    if (token !== initToken) return
    config.value = cfg
    searchMode.value = cfg.mode
    searchStatus.value = { ...cfg.status }
    configLoaded.value = true
  } catch {
    if (token !== initToken) return
    // Degraded mode: no contract, no attribute UI — builtin listing and
    // default search still work.
  }
  if (!sortBy.value && sortAttrs.value.length) {
    sortBy.value = sortAttrs.value.find((a) => a.id === DEFAULT_SORT_ID)?.id ?? sortAttrs.value[0].id
  }
  await reload()
}

onMounted(() => {
  cardObserver = new ResizeObserver((entries) => {
    sizeCards(entries.map((entry) => entry.target as HTMLElement))
    void nextTick(checkLoadMore)
  })
  scrollObserver = new ResizeObserver(checkLoadMore)
  if (scrollEl.value) scrollObserver.observe(scrollEl.value)
  void initGallery()
})

watch(viewerOpen, (open) => {
  if (!open) void nextTick(checkLoadMore)
})

watch(
  () => props.knowledgeBaseId,
  (next, prev) => {
    if (next && next !== prev) void initGallery()
  },
)
</script>

<template>
  <div class="image-gallery">
    <!-- Same shape as the documents tab: what is listed on the left, the
         controls that narrow and order it on the right. -->
    <div class="ig-toolbar">
      <div class="ig-toolbar__summary">
        <span class="ig-toolbar__title">{{ t(`${NS}.allImages`) }}</span>
        <span v-if="configLoaded || total" class="ig-toolbar__count">
          {{ t(isNarrowed ? `${NS}.countFiltered` : `${NS}.count`, { count: total }) }}
        </span>
      </div>

      <div class="ig-toolbar__trailing">
        <t-input
          v-model="keyword"
          :placeholder="t(`${NS}.searchPlaceholder`)"
          :aria-label="t(`${NS}.searchPlaceholder`)"
          clearable
          class="ig-search"
          @enter="onSearchNow"
          @input="onSearchInput"
          @clear="onSearchNow"
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>

        <t-popup
          v-model:visible="sourcePickerVisible"
          trigger="click"
          placement="bottom-right"
          overlay-class-name="gallery-toolbar-popup"
          :overlay-inner-style="{ padding: 0 }"
        >
          <button
            type="button"
            class="ig-tool-btn"
            :class="{ active: sourcePickerVisible || sourceDocIds.length > 0 }"
            :aria-expanded="sourcePickerVisible"
          >
            <t-icon name="file" size="16px" />
            {{ t(`${NS}.sourceFilter`) }}
            <span v-if="sourceDocIds.length" class="ig-tool-btn__count">{{ sourceDocIds.length }}</span>
          </button>
          <template #content>
            <GallerySourcePicker
              :kb-id="props.knowledgeBaseId"
              :visible="sourcePickerVisible"
              :selected="sourceDocIds"
              @update:selected="onSourceDocsChange"
            />
          </template>
        </t-popup>

        <t-popup
          v-model:visible="filterPanelVisible"
          trigger="click"
          placement="bottom-right"
          overlay-class-name="gallery-toolbar-popup"
          :overlay-inner-style="{ padding: 0 }"
        >
          <button
            type="button"
            class="ig-tool-btn"
            :class="{ active: filterPanelVisible || activeFilterCount > 0 }"
            :aria-expanded="filterPanelVisible"
          >
            <t-icon name="filter" size="16px" />
            {{ t(`${NS}.filters`) }}
            <span v-if="activeFilterCount" class="ig-tool-btn__count">{{ activeFilterCount }}</span>
          </button>
          <template #content>
            <section class="ig-filter-panel" :aria-label="t(`${NS}.filters`)">
              <header class="ig-filter-panel__header">
                <strong>{{ t(`${NS}.filters`) }}</strong>
                <button type="button" :disabled="!activeFilterCount" @click="clearFilters">
                  {{ t(`${NS}.clearFilters`) }}
                </button>
              </header>

              <div v-if="searchAttrs.length" class="ig-filter-section">
                <div class="ig-filter-section__title">{{ t(`${NS}.searchIn`) }}</div>
                <div class="ig-filter-section__hint">{{ t(`${NS}.searchInHint`) }}</div>
                <div class="ig-search-fields">
                  <t-checkbox
                    v-for="attr in searchAttrs"
                    :key="attr.id"
                    :checked="activeSearchIds.includes(attr.id)"
                    :disabled="isLastSearchField(attr.id)"
                    :title="attrDescription(attr)"
                    @change="(checked: boolean) => onSearchFieldToggle(attr.id, checked)"
                  >
                    {{ attrLabel(attr) }}
                  </t-checkbox>
                </div>
              </div>

              <div v-if="filterAttrs.length" class="ig-filter-section">
                <div class="ig-filter-section__title">{{ t(`${NS}.attrSection`) }}</div>
                <div class="ig-filter-section__hint">{{ t(`${NS}.attrHint`) }}</div>
                <div v-for="attr in filterAttrs" :key="attr.id" class="ig-attr">
                  <div class="ig-attr__name">
                    {{ attrLabel(attr) }}
                    <t-tooltip v-if="attrDescription(attr)" :content="attrDescription(attr)">
                      <t-icon name="help-circle" size="14px" class="ig-attr__help" />
                    </t-tooltip>
                  </div>
                  <!-- A value list gets one row per value with the three
                       positions side by side; a free-text attribute has
                       nothing to position, so it keeps a keyword box. -->
                  <template v-if="attr.type !== 'keywords'">
                    <div v-for="v in attr.values || []" :key="v.value" class="ig-attr__row">
                      <span class="ig-attr__value" :title="attrValueDescription(attr, v.value)">
                        {{ attrValueLabel(attr, v.value) }}
                      </span>
                      <div class="ig-seg" role="radiogroup" :aria-label="attrValueLabel(attr, v.value)">
                        <button
                          v-for="verdict in VERDICTS"
                          :key="verdict"
                          type="button"
                          role="radio"
                          class="ig-seg__item"
                          :class="['is-' + verdict, { active: verdictOf(attr.id, v.value) === verdict }]"
                          :aria-checked="verdictOf(attr.id, v.value) === verdict"
                          @click="setVerdict(attr.id, v.value, verdict)"
                        >
                          {{ verdictLabel(verdict) }}
                        </button>
                      </div>
                    </div>
                  </template>
                  <t-input
                    v-else
                    :value="keywordsInputValue(attr.id)"
                    clearable
                    :placeholder="t(`${NS}.keywordsPlaceholder`)"
                    @change="(v: string) => onKeywordsInput(attr.id, v)"
                    @enter="(v: string) => onKeywordsInput(attr.id, v)"
                  />
                </div>
              </div>

              <div v-if="!searchAttrs.length && !filterAttrs.length" class="ig-filter-panel__empty">
                {{ t(`${NS}.noAttrs`) }}
              </div>
            </section>
          </template>
        </t-popup>

        <t-popup
          v-if="sortAttrs.length"
          v-model:visible="sortPanelVisible"
          trigger="click"
          placement="bottom-right"
          overlay-class-name="gallery-toolbar-popup"
          :overlay-inner-style="{ padding: 0 }"
        >
          <button
            type="button"
            class="ig-tool-btn ig-sort-trigger"
            :class="{ active: sortPanelVisible }"
            :aria-label="`${t(`${NS}.sort`)}: ${sortFieldLabel}`"
          >
            <t-icon name="filter-sort" size="16px" />
            <span class="ig-sort-trigger__label">{{ t(`${NS}.sort`) }} · {{ sortFieldLabel }}</span>
            <t-icon :name="sortOrder === 'asc' ? 'arrow-up' : 'arrow-down'" size="14px" />
          </button>
          <template #content>
            <div class="ig-sort-panel" role="menu" :aria-label="t(`${NS}.sort`)">
              <section class="ig-sort-group">
                <div class="ig-sort-group__label">{{ t(`${NS}.sortField`) }}</div>
                <div class="ig-sort-group__options">
                  <button
                    v-for="attr in sortAttrs"
                    :key="attr.id"
                    type="button"
                    role="menuitemradio"
                    class="ig-sort-option"
                    :class="{ active: sortBy === attr.id }"
                    :aria-checked="sortBy === attr.id"
                    @click="selectSortField(attr.id)"
                  >
                    <span>{{ attrLabel(attr) }}</span>
                    <t-icon v-if="sortBy === attr.id" name="check" size="14px" />
                  </button>
                </div>
              </section>
              <section class="ig-sort-group">
                <div class="ig-sort-group__label">{{ t(`${NS}.sortOrder`) }}</div>
                <div class="ig-sort-group__options">
                  <button
                    v-for="order in (['desc', 'asc'] as const)"
                    :key="order"
                    type="button"
                    role="menuitemradio"
                    class="ig-sort-option"
                    :class="{ active: sortOrder === order }"
                    :aria-checked="sortOrder === order"
                    @click="selectSortOrder(order)"
                  >
                    <span>{{ t(order === 'asc' ? `${NS}.orderAsc` : `${NS}.orderDesc`) }}</span>
                    <t-icon v-if="sortOrder === order" name="check" size="14px" />
                  </button>
                </div>
              </section>
            </div>
          </template>
        </t-popup>
      </div>
    </div>

    <div ref="scrollEl" class="ig-scroll" :class="{ 'is-empty': !items.length }" @scroll.passive="checkLoadMore">
      <t-loading :loading="loading && !items.length" size="small" class="ig-loading">
        <div v-if="error && !items.length" class="ig-error" role="alert">
          {{ error }}
          <t-button variant="text" @click="reload">{{ t('common.retry') }}</t-button>
        </div>

        <EmptyState
          v-else-if="!items.length && !loading"
          icon="image"
          :title="isNarrowed ? t(`${NS}.emptyFiltered`) : t(`${NS}.empty`)"
          :description="isNarrowed ? '' : t(`${NS}.emptyHint`)"
        >
          <t-button v-if="isNarrowed" variant="outline" @click="clearAll">{{ t(`${NS}.clearFilters`) }}</t-button>
        </EmptyState>

        <div v-else ref="gridEl" class="ig-grid">
          <button
            v-for="(img, idx) in items"
            :key="img.id"
            type="button"
            class="ig-card"
            :class="{ 'is-disabled': !img.is_enabled }"
            @click="openViewer(idx)"
          >
            <div class="ig-card__thumb" :style="{ aspectRatio: imageRatios[img.id] || 4 / 3 }">
              <img
                v-if="thumbUrls[img.id] && !thumbBroken[img.id]"
                :src="thumbUrls[img.id]"
                :alt="img.caption"
                loading="lazy"
                draggable="false"
                @load="onThumbLoad(img.id, $event)"
                @error="onThumbError(img.id)"
              />
              <div v-else-if="thumbBroken[img.id]" class="ig-card__broken">
                <t-icon name="image-error" size="20px" />
                <span>{{ t(`${NS}.imageLoadError`) }}</span>
              </div>
              <span v-if="!img.is_enabled" class="ig-card__badge">
                {{ t(`${NS}.attr.builtin_is_enabled_value_false`) }}
              </span>
            </div>
            <div class="ig-card__meta">
              <div class="ig-card__caption" :class="{ 'is-empty': !img.caption }">
                {{ img.caption || t(`${NS}.noCaption`) }}
              </div>
              <div class="ig-card__source" :title="sourceLabel(img)">
                <t-icon name="file" size="14px" />
                <span>{{ sourceLabel(img) }}</span>
              </div>
            </div>
          </button>
        </div>
      </t-loading>
      <div v-if="items.length" ref="loadMoreEl" class="ig-load-more" aria-live="polite">
        <template v-if="error">
          <span class="ig-error" role="alert">{{ error }}</span>
          <t-button variant="text" @click="loadMore">{{ t('common.retry') }}</t-button>
        </template>
        <t-loading v-else-if="loading" size="small" :text="t('common.loading')" />
        <t-button v-else-if="hasMore" variant="text" @click="loadMore">{{ t('common.loadMore') }}</t-button>
        <span v-else>{{ t('common.noMoreData') }}</span>
      </div>
    </div>

    <GalleryViewer
      v-if="viewerOpen && items.length"
      :items="items"
      :index="viewerIndex"
      :total="total"
      :loading="loading"
      :thumb-urls="thumbUrls"
      :resolve-src="resolveImageSrc"
      :attr-rows="attrRows"
      @select="(i: number) => (viewerIndex = i)"
      @load-more="onViewerLoadMore"
      @close="closeViewer"
      @open-source="onOpenSource"
    />
  </div>
</template>

<style>
/* Popups render outside the component, so their shell is styled unscoped;
   same frame as the documents tab's filter popup. */
.gallery-toolbar-popup .t-popup__content {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  box-shadow: 0 8px 32px rgb(0 0 0 / 10%);
}
</style>

<style scoped lang="less">
.image-gallery {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  min-width: 0;
  container-type: inline-size;
  container-name: image-gallery;
}

// Mirrors .doc-filter-bar in KnowledgeBase.vue so switching tabs keeps the
// toolbar in place.
.ig-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-shrink: 0;
  padding: 0 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  &__summary {
    display: flex;
    align-items: baseline;
    gap: 12px;
    min-width: 0;
    min-height: 32px;
    line-height: 32px;
  }

  &__title {
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    font-weight: 600;
    white-space: nowrap;
  }

  &__count {
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  &__trailing {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
}

.ig-search {
  width: 220px;
  min-width: 100px;

  :deep(.t-input) {
    background: transparent;
    border-color: var(--td-component-stroke);
    border-radius: var(--app-radius-md);
    font-size: var(--app-text-md);
  }
}

.ig-tool-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  white-space: nowrap;
  cursor: pointer;

  &:hover,
  &.active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--app-focus-border);
    outline-offset: 2px;
  }

  &__count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 17px;
    height: 17px;
    padding: 0 2px;
    color: var(--td-brand-color);
    font-weight: 600;
    font-size: var(--app-text-xs);
    font-variant-numeric: tabular-nums;
  }
}

.ig-sort-trigger {
  max-width: 220px;

  &__label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

// Filter panel — same frame and type scale as .doc-filter-panel.
.ig-filter-panel {
  width: 360px;
  max-width: calc(100vw - 32px);
  max-height: min(600px, 80vh);
  overflow-y: auto;
  padding: 16px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);

  button {
    font: inherit;
    cursor: pointer;
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;

    strong {
      font-size: var(--app-text-base);
      font-weight: 600;
    }

    button {
      border: 0;
      padding: 0;
      background: transparent;
      color: var(--td-text-color-secondary);
      font-size: var(--app-text-sm);

      &:hover:not(:disabled) { color: var(--td-brand-color); }
      &:disabled { color: var(--td-text-color-disabled); cursor: default; }
    }
  }

  &__empty {
    padding: 8px 0;
    color: var(--td-text-color-placeholder);
  }
}

.ig-filter-section {
  & + & {
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--td-component-stroke);
  }

  &__title {
    font-weight: 600;
    line-height: 20px;
  }

  &__hint {
    margin: 2px 0 10px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-xs);
    line-height: 17px;
  }
}

.ig-search-fields {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
}

.ig-attr {
  & + & { margin-top: 12px; }

  &__name {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 4px;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
  }

  &__help {
    color: var(--td-text-color-placeholder);
    cursor: help;
  }

  &__row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 32px;
  }

  &__value {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

// Three-position switch, drawn like the documents tab's view toggle.
.ig-seg {
  display: inline-flex;
  flex-shrink: 0;
  padding: 2px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);

  &__item {
    height: 24px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--app-radius-xs);
    background: transparent;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    white-space: nowrap;
    transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

    &:hover { color: var(--td-text-color-primary); }

    &.active {
      background: var(--td-bg-color-container);
      color: var(--td-text-color-primary);
      box-shadow: 0 1px 3px rgb(0 0 0 / 8%);
    }

    &.is-off.active { color: var(--td-error-color); }
    &.is-on.active { color: var(--td-brand-color); }

    &:focus-visible {
      outline: 2px solid var(--app-focus-border);
      outline-offset: 1px;
    }
  }
}

// Sort panel — same shape as .document-sort-panel.
.ig-sort-panel {
  width: 280px;
  max-width: calc(100vw - 32px);
  padding: 6px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
}

.ig-sort-group {
  padding: 7px 6px 8px;

  & + & { border-top: 1px solid var(--td-component-stroke); }

  &__label {
    padding: 0 4px 6px;
    font-size: var(--app-text-md);
    font-weight: 600;
    line-height: 20px;
  }

  &__options {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 4px;
  }
}

.ig-sort-option {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
  height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  cursor: pointer;

  &:hover { background: var(--td-bg-color-secondarycontainer); }

  &.active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

// Grid
.ig-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 16px 0;

  &.is-empty {
    display: flex;
    flex-direction: column;
  }
}

.ig-loading {
  display: flex;
  flex-direction: column;

  .is-empty & { min-height: 100%; }
}

.ig-error {
  padding: 16px 0;
  color: var(--td-error-color);
}

.ig-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(180px, 100%), 1fr));
  grid-auto-rows: 1px;
  gap: 12px;
  align-items: start;
  align-content: start;
}

.ig-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgb(0 0 0 / 6%);
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-component-stroke) 55%, var(--td-brand-color));
    box-shadow: 0 4px 14px rgb(0 0 0 / 7%);

    .ig-card__thumb img { transform: scale(1.03); }
  }

  &:focus-visible {
    outline: 2px solid var(--app-focus-border);
    outline-offset: 2px;
  }

  &.is-disabled .ig-card__thumb img { opacity: 0.55; }

  &__thumb {
    position: relative;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    background: var(--td-bg-color-secondarycontainer);

    img {
      width: 100%;
      height: 100%;
      object-fit: contain;
      transition: transform var(--app-motion-slow) ease;
    }
  }

  &__broken {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding: 0 8px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    text-align: center;
  }

  &__badge {
    position: absolute;
    top: 8px;
    left: 8px;
    padding: 0 6px;
    border-radius: var(--app-radius-xs);
    background: rgb(0 0 0 / 55%);
    color: #fff;
    font-size: var(--app-text-xs);
    line-height: 18px;
  }

  &__meta {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px 12px 12px;
  }

  &__caption {
    display: -webkit-box;
    min-height: calc(2 * 1.5em);
    overflow: hidden;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    line-height: 1.5;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;

    &.is-empty { color: var(--td-text-color-placeholder); }
  }

  &__source {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);

    .t-icon { flex-shrink: 0; }

    span {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}

.ig-load-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 56px;
  padding: 16px 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

@container image-gallery (max-width: 780px) {
  .ig-toolbar { flex-wrap: wrap; gap: 12px; }
  .ig-toolbar__trailing { width: 100%; }
  .ig-search { flex: 1; width: auto; }
}
</style>
