<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import { buildProtectedFileRequest } from '@/utils/protectedFileAccess'

const props = defineProps<{
  knowledgeBaseId: string
}>()

const { t, te } = useI18n()

// ---------------------------------------------------------------------------
// Data state
// ---------------------------------------------------------------------------
const loading = ref(false)
const error = ref('')
const items = ref<ImageAsset[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(24)

const keyword = ref('')
const sortBy = ref('')
const sortOrder = ref<'asc' | 'desc'>('desc')

// The two scope switches in the toolbar row. Each has the same shape: the
// inclusive position drops the constraint entirely, the custom position
// opens the panel that edits it. "筛选：全显示" therefore really is a
// switch — when it reads "全显示", no attribute constraint is sent at all.
const searchScope = ref<'all' | 'custom'>('all')
const filterScope = ref<'all' | 'custom'>('all')

// Which settings panel is on screen. A panel opens as a popover under the
// arrow that summoned it, and a pin moves it into the right-hand rail,
// where the two panels take turns as tabs instead of both eating space.
const openPanel = ref<'search' | 'filter' | ''>('')
const pinnedPanel = ref<'search' | 'filter' | ''>('')
const panelTab = ref<'search' | 'filter'>('search')
// Where the floating panel hangs: captured from the arrow that opened it,
// so it sits directly beneath that control instead of at a guessed spot.
const panelPos = ref<{ top: number; left: number } | null>(null)
const rootEl = ref<HTMLElement | null>(null)

// Attribute selections: namespaced attr id -> selected allowed values (OR
// within the attribute, AND across attributes). Free-text attributes have no
// value list to pick from, so they carry their own literal selection.
const attrSelections = ref<Record<string, string[]>>({})

// Per-value verdicts, namespaced attr id -> value -> "off" | "on". An absent
// key is the middle position: an image carrying that value stays exactly as
// visible as it was, which is what makes the default state show everything.
const attrVerdicts = ref<Record<string, Record<string, string>>>({})

// ---------------------------------------------------------------------------
// Gallery contract (self-describing, fetched once per mount)
//
// Everything the UI offers — filter panel sections, searchable fields, sort
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
// Viewer state
// ---------------------------------------------------------------------------
const viewerOpen = ref(false)
const viewerIndex = ref(0)
const imageFailed = ref(false)

const current = computed<ImageAsset | null>(() =>
  viewerOpen.value && items.value.length ? items.value[viewerIndex.value] ?? null : null,
)

// ---------------------------------------------------------------------------
// Image URL resolution
//
// Backend returns `resource://` handles (internal storage handles) which the
// browser cannot render directly. They must be fetched through the KB file
// proxy (buildProtectedFileRequest) and turned into object URLs first.
// Normal http(s) URLs pass through untouched.
// ---------------------------------------------------------------------------
const thumbUrls = ref<Record<string, string>>({})
const thumbBroken = ref<Record<string, boolean>>({})
const viewerUrl = ref('')
const createdBlobs = new Set<string>()

function isRenderable(url: string): boolean {
  return !!url && !/^(resource|provider|storage):\/\//i.test(url)
}

async function resolveImageSrc(rawUrl: string): Promise<string> {
  if (!rawUrl || isRenderable(rawUrl)) return rawUrl
  const req = buildProtectedFileRequest(rawUrl, {
    mode: 'knowledgeBase',
    kbId: props.knowledgeBaseId,
  })
  if (!req) return rawUrl
  try {
    const resp = await fetch(req.url, { headers: req.headers })
    if (!resp.ok) return rawUrl
    const blob = await resp.blob()
    const objectUrl = URL.createObjectURL(blob)
    createdBlobs.add(objectUrl)
    return objectUrl
  } catch {
    return rawUrl
  }
}

function thumbUrl(img: ImageAsset): string {
  return thumbUrls.value[img.id] || img.url
}

async function resolveThumbnails() {
  const prev = thumbUrls.value
  const next: Record<string, string> = {}
  for (const img of items.value) {
    next[img.id] = await resolveImageSrc(img.url)
  }
  // Revoke blob URLs that are no longer on screen to avoid leaks.
  for (const id of Object.keys(prev)) {
    const prevUrl = prev[id]
    if (!(id in next) && createdBlobs.has(prevUrl)) {
      URL.revokeObjectURL(prevUrl)
      createdBlobs.delete(prevUrl)
    }
  }
  thumbUrls.value = next
  thumbBroken.value = {}
}

function onThumbError(id: string) {
  thumbBroken.value = { ...thumbBroken.value, [id]: true }
}

let viewerToken = 0

watch(
  () => current.value,
  async (img) => {
    if (!img) {
      viewerUrl.value = ''
      return
    }
    // Opening the viewer renders the <img> at once, while the blob the
    // browser can actually display only exists after the proxy fetch below.
    // In the meantime the element still shows the previous (or empty)
    // source, whose error event flipped the failure flag — and the real
    // image then arrived to a viewer already showing its failure state.
    // The token keeps a stale fetch from winning a fast navigation race,
    // and the flag is reset here, after the real source is in hand.
    const token = ++viewerToken
    const url = await resolveImageSrc(img.url)
    if (token !== viewerToken) return
    imageFailed.value = false
    viewerUrl.value = url
  },
)

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

const currentAttrs = computed(() => {
  if (!current.value) return [] as Array<{ key: string; label: string; value: string }>
  return Object.entries(current.value.attrs).map(([name, raw]) => {
    const attr = findAttrByRawName(name)
    return {
      key: name,
      label: attr ? attrLabel(attr) : name,
      value: displayObservedValue(attr, raw),
    }
  })
})

// ---------------------------------------------------------------------------
// Filter verdicts
//
// One attribute value moves through three positions by click: neutral (leave
// those images alone), "off" (hide them), "on" (show them whatever else says).
// The middle position is the one a user starts at and returns to, so a value
// the user never touched is absent from the map rather than stored as a word,
// and never reaches the server.
// ---------------------------------------------------------------------------
// The filter panel's own switch: off means the verdicts are not imposed at
// all, which is what the toolbar's 全显示 position shows.
const filterScopeOn = computed({
  get: () => filterScope.value === 'custom',
  set: (on: boolean) => onFilterScopeChange(on ? 'custom' : 'all'),
})

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

function verdictOf(attrId: string, value: string): Verdict {
  return (attrVerdicts.value[attrId]?.[value] as Verdict) || 'default'
}

function setVerdict(attrId: string, value: string, verdict: Verdict): void {
  const perValue = { ...(attrVerdicts.value[attrId] || {}) }
  if (verdict === 'default') delete perValue[value]
  else perValue[value] = verdict
  attrVerdicts.value = { ...attrVerdicts.value, [attrId]: perValue }
  // A verdict the user just expressed must take effect immediately: staying
  // in "全显示" while the panel shows an off/on would silently ignore it.
  // Clearing the last meaningful verdict returns to "全显示", since custom
  // mode with no rules constrains nothing anyway.
  const hasRules = Object.values(attrVerdicts.value).some((perValueInner) =>
    Object.values(perValueInner).some((v) => v === 'off' || v === 'on'),
  )
  filterScope.value = hasRules ? 'custom' : 'all'
  resetPageAndReload()
}

function verdictLabel(v: Verdict): string {
  if (v === 'off') return t('knowledgeEditor.wikiBrowser.gallery.verdictOff')
  if (v === 'on') return t('knowledgeEditor.wikiBrowser.gallery.verdictOn')
  return t('knowledgeEditor.wikiBrowser.gallery.verdictDefault')
}

// ---------------------------------------------------------------------------
// Settings panels
//
// Both panels answer the same question — what may the gallery show — and both
// open the same way: an arrow beside the toolbar control summons them as a
// popover, and the pin on the panel's header moves it into the right-hand
// rail. Pinning one keeps the other out of the rail, so the two never split
// the image area between them.
// ---------------------------------------------------------------------------
function openSettingsPanel(panel: 'search' | 'filter', ev?: MouseEvent): void {
  // A pinned rail already holds both panels as tabs; an arrow click merely
  // turns to the requested one instead of spawning a second surface.
  if (pinnedPanel.value) {
    panelTab.value = panel
    openPanel.value = ''
    return
  }
  // The arrow is a toggle: a second press on the same arrow retracts the
  // panel, which is also why its icon points up while the panel is out.
  if (openPanel.value === panel) {
    openPanel.value = ''
    return
  }
  openPanel.value = panel
  panelTab.value = panel
  if (ev) updatePanelPosition(ev)
}

/** Anchor the floating panel just below the arrow that summoned it. */
function updatePanelPosition(ev: MouseEvent): void {
  const root = rootEl.value?.getBoundingClientRect()
  const btn = (ev.currentTarget as HTMLElement | null)?.getBoundingClientRect()
  if (!root || !btn) return
  const width = 320
  const left = Math.max(8, Math.min(btn.left - root.left, root.width - width - 12))
  panelPos.value = { top: btn.bottom - root.top + 6, left }
}

const floatingStyle = computed(() =>
  panelPos.value
    ? { top: `${panelPos.value.top}px`, left: `${panelPos.value.left}px`, right: 'auto' }
    : {},
)

// Any click landing outside the floating panel and outside the arrows closes
// it: moving on to another toolbar control means leaving the panel. Clicks
// inside the panel itself, and on the toggling arrows, are left alone.
function onDocClick(e: MouseEvent): void {
  if (!openPanel.value || pinnedPanel.value) return
  const target = e.target as HTMLElement | null
  if (!target) return
  if (target.closest('.ig-panel-slot')) return
  if (target.closest('.ig-scope-arrow')) return
  openPanel.value = ''
}

function togglePin(panel: 'search' | 'filter'): void {
  if (pinnedPanel.value === panel) {
    pinnedPanel.value = ''
    return
  }
  pinnedPanel.value = panel
  panelTab.value = panel
  openPanel.value = ''
}

function onFilterScopeChange(value: string | number | boolean): void {
  filterScope.value = value === 'custom' ? 'custom' : 'all'
  resetPageAndReload()
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------
function buildParams(): ImageListParams {
  const attrFilters: Record<string, string[]> = {}
  for (const [id, values] of Object.entries(attrSelections.value)) {
    if (values && values.length) attrFilters[id] = values
  }
  // The filter switch decides whether verdicts are imposed at all: reading
  // "全显示" means the panel's verdicts are not applied, whatever is set in it.
  const rules = filterScope.value === 'custom' ? activeRules.value : {}
  const params: ImageListParams = {
    keyword: keyword.value.trim() || undefined,
    searchIn: activeSearchIds.value,
    sortBy: sortBy.value || undefined,
    sortOrder: sortOrder.value,
    attrFilters: Object.keys(attrFilters).length ? attrFilters : undefined,
    attrRules: Object.keys(rules).length ? rules : undefined,
    page: page.value,
    pageSize: pageSize.value,
  }
  return params
}

async function reload() {
  if (!props.knowledgeBaseId) return
  // Searching with every field switched off would silently fall back to the
  // server default — not what a custom-mode user asked for. Short-circuit.
  if (keyword.value.trim() && activeSearchIds.value.length === 0) {
    items.value = []
    total.value = 0
    return
  }
  loading.value = true
  error.value = ''
  try {
    const res = await listGalleryImages(props.knowledgeBaseId, buildParams())
    items.value = res.items
    total.value = res.total
    if (viewerOpen.value && viewerIndex.value >= res.items.length) closeViewer()
    void resolveThumbnails()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    items.value = []
    total.value = 0
    thumbUrls.value = {}
  } finally {
    loading.value = false
  }
}

function resetPageAndReload() {
  page.value = 1
  reload()
}

// ---------------------------------------------------------------------------
// Personal state persistence (mode + per-field search toggles)
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

// The search panel's master switch: on searches every eligible field, off
// restricts the search to the fields the user switched on below it.
const searchAllOn = computed({
  get: () => searchMode.value === 'all',
  set: (on: boolean) => onSearchModeChange(on ? 'all' : 'custom'),
})

function onSearchModeChange(value: string | number | boolean) {
  searchMode.value = value === 'custom' ? 'custom' : 'all'
  persistSearchPrefs()
  resetPageAndReload()
}

// Reconcile the whole on/off map from one checkbox-group change, then
// persist and re-query. In "all" mode every field is searched and the
// checkboxes are only decorative (dimmed): touching one means the user
// wants a custom selection, so the master switch drops to custom with
// every field still on except the one just unticked.
function onSearchFieldsChange(vals: Array<string | number | boolean>) {
  const next: Record<string, string> = { ...searchStatus.value }
  for (const attr of searchAttrs.value) {
    next[attr.id] = vals.includes(attr.id) ? 'on' : 'off'
  }
  searchStatus.value = next
  if (searchMode.value === 'all') searchMode.value = 'custom'
  persistSearchPrefs()
  resetPageAndReload()
}

// ---------------------------------------------------------------------------
// Filter interactions
// ---------------------------------------------------------------------------
function onSearch() {
  resetPageAndReload()
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => resetPageAndReload(), 350)
}

function onAttrGroupChange(attrId: string, values: Array<string | number | boolean>) {
  attrSelections.value = {
    ...attrSelections.value,
    [attrId]: values.map((v) => String(v)),
  }
  resetPageAndReload()
}

function onKeywordsInput(attrId: string, raw: string) {
  const values = raw
    .split(/[,，;；\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
  attrSelections.value = { ...attrSelections.value, [attrId]: values }
  resetPageAndReload()
}

function keywordsInputValue(attrId: string): string {
  return (attrSelections.value[attrId] || []).join(', ')
}

// Apply one verdict to every value of every value-typed filter attribute.
// The "-" position clears all verdicts, which also drops the scope back to
// "全显示": custom mode with no rules constrains nothing.
function setAllVerdicts(verdict: Verdict): void {
  const next: Record<string, Record<string, string>> = {}
  for (const attr of filterAttrs.value) {
    if (attr.type === 'keywords') continue
    const perValue: Record<string, string> = { ...(attrVerdicts.value[attr.id] || {}) }
    for (const v of attr.values || []) {
      if (verdict === 'default') delete perValue[v.value]
      else perValue[v.value] = verdict
    }
    if (Object.keys(perValue).length > 0) next[attr.id] = perValue
  }
  attrVerdicts.value = next
  const hasRules = Object.values(next).some((perValue) =>
    Object.values(perValue).some((v) => v === 'off' || v === 'on'),
  )
  filterScope.value = hasRules ? 'custom' : 'all'
  resetPageAndReload()
}

function hasActiveFilters(): boolean {
  return !!keyword.value.trim() || Object.values(attrSelections.value).some((v) => v.length)
}

function onPageChange(next: number) {
  page.value = next
  reload()
}

// ---------------------------------------------------------------------------
// Viewer
// ---------------------------------------------------------------------------
function openViewer(index: number) {
  viewerIndex.value = index
  imageFailed.value = false
  viewerOpen.value = true
}

function closeViewer() {
  viewerOpen.value = false
}

function prevImage() {
  if (!items.value.length) return
  viewerIndex.value = (viewerIndex.value - 1 + items.value.length) % items.value.length
  imageFailed.value = false
}

function nextImage() {
  if (!items.value.length) return
  viewerIndex.value = (viewerIndex.value + 1) % items.value.length
  imageFailed.value = false
}

function onViewerKey(e: KeyboardEvent) {
  if (!viewerOpen.value) return
  if (e.key === 'Escape') closeViewer()
  else if (e.key === 'ArrowLeft') prevImage()
  else if (e.key === 'ArrowRight') nextImage()
}

function sourceLabel(img: ImageAsset): string {
  return img.source_name || img.knowledge_id
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------
onMounted(() => {
  document.addEventListener('click', onDocClick)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
})

onMounted(async () => {
  try {
    const cfg = await fetchGalleryConfig(props.knowledgeBaseId)
    config.value = cfg
    searchMode.value = cfg.mode
    searchStatus.value = { ...cfg.status }
    configLoaded.value = true
  } catch {
    // Degraded mode: no contract, no attribute UI — builtin listing and
    // default search still work.
  }
  if (!sortBy.value && sortAttrs.value.length) {
    sortBy.value = sortAttrs.value[0].id
  }
  await reload()
})
</script>

<template>
  <div ref="rootEl" class="image-gallery" @keydown="onViewerKey">
    <div class="ig-layout" :class="{ 'has-rail': !!pinnedPanel }">
      <!--
        One settings panel, two placements: pinning it moves it out of the
        grid and into the right-hand rail, where the two panels take turns as
        tabs. Only one can hold the rail, so the image area never has to share
        its width with both of them.
      -->
      <aside
        v-if="openPanel || pinnedPanel"
        class="ig-panel-slot"
        :class="{ 'is-floating': openPanel && !pinnedPanel }"
        :style="openPanel && !pinnedPanel ? floatingStyle : undefined"
      >
        <div class="ig-panel-head">
          <t-tabs v-model="panelTab" class="ig-panel-tabs">
            <!-- Same order as the toolbar: search on the left, filter on the right. -->
            <t-tab-panel value="search" :label="t('knowledgeEditor.wikiBrowser.gallery.panelSearch')" />
            <t-tab-panel value="filter" :label="t('knowledgeEditor.wikiBrowser.gallery.panelFilter')" />
          </t-tabs>
          <button
            class="ig-pin"
            :class="{ 'is-pinned': !!pinnedPanel }"
            :title="pinnedPanel === panelTab ? t('knowledgeEditor.wikiBrowser.gallery.unpin') : t('knowledgeEditor.wikiBrowser.gallery.pin')"
            @click="togglePin(panelTab)"
          >
            <t-icon :name="pinnedPanel === panelTab ? 'pin-filled' : 'pin'" />
          </button>
        </div>

        <div class="ig-panel-body">
          <template v-if="panelTab === 'filter'">
            <div class="ig-row">
              <span class="ig-row-label">{{ t('knowledgeEditor.wikiBrowser.gallery.enableFilter') }}</span>
              <t-switch v-model="filterScopeOn" />
            </div>

            <div v-if="filterAttrs.length" class="ig-attr-blocks">
              <div v-for="attr in filterAttrs" :key="attr.id" class="ig-attr-block">
                <div class="ig-attr-name" :title="attrDescription(attr)">{{ attrLabel(attr) }}</div>

                <!--
                  An attribute that declares values gets one row per value,
                  each with three positions: leave the images carrying it
                  alone, hide them, or force them back in.
                -->
                <div v-if="attr.type !== 'keywords'" class="ig-verdict-rows" :class="{ 'is-idle': !filterScopeOn }">
                  <div v-for="v in attr.values || []" :key="v.value" class="ig-verdict-row">
                    <t-tooltip :content="attrValueDescription(attr, v.value)">
                      <span class="ig-verdict-value">{{ attrValueLabel(attr, v.value) }}</span>
                    </t-tooltip>
                    <div class="ig-verdict-group">
                      <button
                        v-for="verdict in VERDICTS"
                        :key="verdict"
                        type="button"
                        class="ig-verdict"
                        :class="['is-' + verdict, { 'is-active': verdictOf(attr.id, v.value) === verdict }]"
                        @click="setVerdict(attr.id, v.value, verdict)"
                      >
                        {{ verdictLabel(verdict) }}
                      </button>
                    </div>
                  </div>
                </div>
                <!--
                  A free-text attribute has no value list to position, so it
                  keeps the plain keyword box.
                -->
                <t-input
                  v-else
                  :value="keywordsInputValue(attr.id)"
                  clearable
                  class="ig-keywords"
                  :placeholder="t('knowledgeEditor.wikiBrowser.gallery.keywordsPlaceholder')"
                  @change="(v: string) => onKeywordsInput(attr.id, v)"
                  @enter="(v: string) => onKeywordsInput(attr.id, v)"
                />
              </div>
            </div>
            <div v-else class="ig-no-attrs">{{ t('knowledgeEditor.wikiBrowser.gallery.noAttrs') }}</div>

            <!--
              One row to speak for every attribute at once: the same three
              positions as a per-value row, applied to all of them in one
              click.
            -->
            <div class="ig-row ig-apply-all">
              <span class="ig-row-label">{{ t('knowledgeEditor.wikiBrowser.gallery.applyAll') }}</span>
              <div class="ig-verdict-group">
                <button
                  v-for="verdict in VERDICTS"
                  :key="verdict"
                  type="button"
                  class="ig-verdict"
                  :class="['is-' + verdict]"
                  @click="setAllVerdicts(verdict)"
                >
                  {{ verdictLabel(verdict) }}
                </button>
              </div>
            </div>
          </template>

          <template v-else>
            <div class="ig-row">
              <span class="ig-row-label">{{ t('knowledgeEditor.wikiBrowser.gallery.searchAll') }}</span>
              <t-switch v-model="searchAllOn" />
            </div>

            <div v-if="searchAttrs.length">
              <!--
                The checkboxes stay visible next to the master switch like the
                filter panel's verdicts do. In "all" mode they are dimmed —
                every field is searched, so the ticks carry no weight — yet
                still clickable: touching one drops the mode to custom. No
                caption above them: showing and hiding it would shift them
                when the switch is flipped.
              -->
              <t-checkbox-group
                :value="activeSearchIds"
                class="ig-search-fields"
                :class="{ 'is-idle': searchMode === 'all' }"
                @change="(vals: Array<string | number | boolean>) => onSearchFieldsChange(vals)"
              >
                <t-checkbox v-for="attr in searchAttrs" :key="attr.id" :value="attr.id" :label="attrLabel(attr)" />
              </t-checkbox-group>
            </div>
          </template>
        </div>
      </aside>

      <!-- Main content -->
      <section class="ig-main">
        <!--
          Everything that controls the list sits on one horizontal bar, so
          the image area below keeps all the vertical space it can get. The
          two scope switches read as 全显示 / 自定义: the inclusive position
          drops the constraint, and only the custom one has an arrow that
          opens the panel editing it.
        -->
        <div class="ig-toolbar">
          <t-input
            v-model="keyword"
            :placeholder="t('knowledgeEditor.wikiBrowser.gallery.searchPlaceholder')"
            clearable
            class="ig-search"
            @enter="onSearch"
            @input="onSearchInput"
            @clear="onSearch"
          >
            <template #prefix-icon><t-icon name="search" /></template>
          </t-input>

          <div class="ig-scope">
            <span class="ig-scope-name">{{ t('knowledgeEditor.wikiBrowser.gallery.searchScope') }}</span>
            <!--
              The scope word is a status, not a control: it names the mode
              the search is in, and only the arrow beside it opens the panel
              that changes it.
            -->
            <span class="ig-scope-value">
              {{ searchScope === 'all' ? t('knowledgeEditor.wikiBrowser.gallery.scopeAll') : t('knowledgeEditor.wikiBrowser.gallery.scopeCustom') }}
            </span>
            <button
              class="ig-scope-arrow"
              :title="t('knowledgeEditor.wikiBrowser.gallery.editSearch')"
              @click="openSettingsPanel('search', $event)"
            >
              <t-icon :name="openPanel === 'search' && !pinnedPanel ? 'chevron-up' : 'chevron-down'" />
            </button>
          </div>

          <div class="ig-scope">
            <span class="ig-scope-name">{{ t('knowledgeEditor.wikiBrowser.gallery.filterScope') }}</span>
            <span class="ig-scope-value">
              {{ filterScope === 'all' ? t('knowledgeEditor.wikiBrowser.gallery.filterOff') : t('knowledgeEditor.wikiBrowser.gallery.filterOn') }}
            </span>
            <button
              class="ig-scope-arrow"
              :title="t('knowledgeEditor.wikiBrowser.gallery.editFilter')"
              @click="openSettingsPanel('filter', $event)"
            >
              <t-icon :name="openPanel === 'filter' && !pinnedPanel ? 'chevron-up' : 'chevron-down'" />
            </button>
          </div>

          <span class="ig-scope-sep" />

          <t-select v-if="sortAttrs.length" v-model="sortBy" class="ig-sort" @change="resetPageAndReload">
            <t-option v-for="attr in sortAttrs" :key="attr.id" :value="attr.id" :label="attrLabel(attr)" />
          </t-select>

          <t-button theme="default" variant="outline" class="ig-order" @click="sortOrder = sortOrder === 'asc' ? 'desc' : 'asc'; resetPageAndReload()">
            <t-icon :name="sortOrder === 'asc' ? 'arrow-up' : 'arrow-down'" />
            {{ sortOrder === 'asc' ? t('knowledgeEditor.wikiBrowser.gallery.orderAsc') : t('knowledgeEditor.wikiBrowser.gallery.orderDesc') }}
          </t-button>

          <span class="ig-count">{{ t('knowledgeEditor.wikiBrowser.gallery.count', { count: total }) }}</span>
        </div>

        <t-loading :loading="loading" class="ig-loading-area">
          <div v-if="error" class="ig-error">{{ error }}</div>

          <div v-else-if="!items.length" class="ig-empty">
            {{ hasActiveFilters()
              ? t('knowledgeEditor.wikiBrowser.gallery.emptyFiltered')
              : t('knowledgeEditor.wikiBrowser.gallery.empty') }}
          </div>

          <div v-else class="ig-grid">
            <button
              v-for="(img, idx) in items"
              :key="img.id"
              type="button"
              class="ig-card"
              @click="openViewer(idx)"
            >
              <div class="ig-card-thumb">
                <img
                  v-if="!thumbBroken[img.id]"
                  :src="thumbUrl(img)"
                  :alt="img.caption"
                  loading="lazy"
                  @error="onThumbError(img.id)"
                />
                <div v-else class="ig-thumb-error">
                  {{ t('knowledgeEditor.wikiBrowser.gallery.imageLoadError') }}
                </div>
              </div>
              <div class="ig-card-meta">
                <div class="ig-card-caption">{{ img.caption || t('knowledgeEditor.wikiBrowser.gallery.noCaption') }}</div>
                <div class="ig-card-source">{{ sourceLabel(img) }}</div>
              </div>
            </button>
          </div>
        </t-loading>

        <t-pagination
          v-if="total > pageSize"
          :total="total"
          :page-size="pageSize"
          :current="page"
          class="ig-pagination"
          @current-change="onPageChange"
        />
      </section>
    </div>

    <!-- Single-image viewer -->
    <div v-if="viewerOpen && current" class="ig-viewer-overlay" @click.self="closeViewer">
      <div class="ig-viewer">
        <button class="ig-viewer-close" :title="t('knowledgeEditor.wikiBrowser.gallery.viewerClose')" @click="closeViewer">
          <t-icon name="close" />
        </button>
        <button class="ig-nav ig-nav-prev" :title="t('knowledgeEditor.wikiBrowser.gallery.prev')" @click="prevImage">
          <t-icon name="chevron-left" />
        </button>

        <div class="ig-viewer-image">
          <!--
            Rendered only once a displayable source exists: the raw
            resource:// handle (or an empty string) would fire the error
            handler and mask the image that is still being fetched.
          -->
          <img v-if="viewerUrl && !imageFailed" :src="viewerUrl" :alt="current.caption" @error="imageFailed = true" />
          <div v-else-if="imageFailed" class="ig-viewer-image-error">{{ t('knowledgeEditor.wikiBrowser.gallery.imageLoadError') }}</div>
        </div>

        <button class="ig-nav ig-nav-next" :title="t('knowledgeEditor.wikiBrowser.gallery.next')" @click="nextImage">
          <t-icon name="chevron-right" />
        </button>

        <aside class="ig-viewer-info">
          <h3>{{ t('knowledgeEditor.wikiBrowser.gallery.attributes') }}</h3>
          <div v-if="currentAttrs.length">
            <div v-for="a in currentAttrs" :key="a.key" class="ig-info-row">
              <span class="ig-info-key">{{ a.label }}</span>
              <span class="ig-info-val">{{ a.value }}</span>
            </div>
          </div>
          <div v-else class="ig-info-muted">{{ t('knowledgeEditor.wikiBrowser.gallery.noAttrs') }}</div>

          <h3>{{ t('knowledgeEditor.wikiBrowser.gallery.caption') }}</h3>
          <p class="ig-info-text">{{ current.caption || t('knowledgeEditor.wikiBrowser.gallery.noCaption') }}</p>

          <h3>{{ t('knowledgeEditor.wikiBrowser.gallery.ocr') }}</h3>
          <p class="ig-info-text">{{ current.ocr_text || t('knowledgeEditor.wikiBrowser.gallery.noOcr') }}</p>

          <h3>{{ t('knowledgeEditor.wikiBrowser.gallery.source') }}</h3>
          <p class="ig-info-text">
            {{ sourceLabel(current) }}
            <span class="ig-info-sub">{{ current.knowledge_id }}</span>
          </p>
        </aside>
      </div>
    </div>
  </div>
</template>

<style scoped>
.image-gallery {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 16px;
  box-sizing: border-box;
}

.ig-layout {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;
  flex: 1;
  min-height: 0;
}

/* A pinned panel takes the right column; the image area keeps the rest.
   The panel precedes the main section in the DOM, so the columns it lands
   in are pinned down explicitly instead of left to source order. */
.ig-layout.has-rail {
  grid-template-columns: minmax(0, 1fr) 300px;
}
.ig-layout.has-rail .ig-main {
  grid-row: 1;
  grid-column: 1;
}
.ig-layout.has-rail .ig-panel-slot {
  grid-row: 1;
  grid-column: 2;
}

/* -------------------------------------------------------------------------
   Settings panel
   One element, two placements: pinned it sits in the rail, otherwise it is
   lifted out of the grid and floats over the image area near its toolbar
   control. Keeping a single element means the two never drift apart.
   ------------------------------------------------------------------------- */
.ig-panel-slot {
  display: none;
  align-self: start;
  max-height: 100%;
}
.ig-layout.has-rail .ig-panel-slot {
  display: block;
}

.ig-panel-slot.is-floating {
  display: block;
  position: absolute;
  top: 92px;
  right: 28px;
  width: 320px;
  z-index: 20;
  max-height: calc(100% - 120px);
  overflow: auto;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: var(--td-radius-medium);
  box-shadow: var(--td-shadow-card));
}

.ig-panel-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  /* No bottom border here: the tab bar already draws one, and a second
     divider under it just reads as visual noise. */
}
.ig-panel-tabs {
  flex: 1 1 0;
  min-width: 0;
}
.ig-pin {
  /* The tab bar would otherwise stretch and shove the pin out of the head. */
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
}
.ig-pin:hover {
  background: var(--td-bg-color-container-hover);
}
.ig-pin.is-pinned {
  color: var(--td-brand-color);
}

.ig-panel-body {
  padding: 12px;
}

.ig-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.ig-row-label {
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}
.ig-hint {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}
.ig-attr-blocks {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.ig-attr-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ig-attr-name {
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}
.ig-verdict-rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
/* A gated block reads as inactive: dimmed text and buttons, but still
   clickable so the first touch can activate the master switch. */
.ig-verdict-rows.is-idle,
.ig-search-fields.is-idle {
  opacity: 0.4;
}
.ig-search-fields {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.ig-verdict-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.ig-verdict-value {
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  cursor: default;
}
.ig-verdict-group {
  display: inline-flex;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);
  overflow: hidden;
}
.ig-verdict {
  min-width: 30px;
  padding: 2px 6px;
  border: none;
  border-right: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container);
  font-size: var(--app-text-sm);
  line-height: 20px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
}
.ig-verdict:last-child {
  border-right: none;
}
.ig-verdict:hover {
  background: var(--td-bg-color-container-hover);
}
.ig-verdict.is-active.is-default {
  background: var(--td-bg-color-secondary);
  color: var(--td-text-color-primary);
  font-weight: 600;
}
.ig-verdict.is-active.is-off {
  background: var(--td-error-color-1);
  color: var(--td-error-color);
  font-weight: 600;
}
.ig-verdict.is-active.is-on {
  background: var(--td-success-color-1);
  color: var(--td-success-color);
  font-weight: 600;
}

/* Toolbar: one row, so nothing steals vertical space from the grid. */
.ig-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
.ig-search {
  width: 200px;
  max-width: 260px;
}
.ig-scope {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.ig-scope-name {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}
.ig-scope-value {
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  white-space: nowrap;
}
.ig-scope-arrow {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 28px;
  padding: 0;
  border: 1px solid var(--td-component-border);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
}
.ig-scope-arrow:hover:not(:disabled) {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
}
.ig-scope-arrow:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.ig-scope-sep {
  width: 1px;
  height: 20px;
  margin: 0 2px;
  background: var(--td-component-border);
}
.ig-sort {
  width: 140px;
}
.ig-order {
  white-space: nowrap;
}
/* The button wraps its content in .t-button__text, an inline-flex box whose
   default stretch pins the explicitly-sized 16px icon to the top of the 22px
   line box, so the arrow reads as riding high; center it instead. */
.ig-order :deep(.t-button__text) {
  align-items: center;
}
.ig-count {
  margin-left: auto;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}

.ig-filter-group {
  margin-bottom: 18px;
}
.ig-filter-label {
  display: block;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  margin-bottom: 8px;
}
.ig-search-hint {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  margin-top: 6px;
}
.ig-search-fields {
  margin-top: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ig-attr-filter {
  margin-bottom: 12px;
}
.ig-attr-name {
  font-size: var(--app-text-md);
  font-weight: 500;
  margin-bottom: 4px;
}
.ig-no-attrs {
  font-size: var(--app-text-md);
}
.ig-apply-all {
  margin-top: 4px;
}
.ig-apply-all .ig-verdict-group {
  flex-shrink: 0;
}

/* Main */
.ig-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.ig-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.ig-search {
  flex: 1;
  min-width: 220px;
}
.ig-sort {
  width: 160px;
}
.ig-count {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}
.ig-loading-area {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.ig-error {
  color: var(--td-error-color);
  padding: 16px 0;
}
.ig-empty {
  color: var(--td-text-color-placeholder);
  padding: 48px 0;
  text-align: center;
}

/* Grid */
.ig-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
  gap: 14px;
}
.ig-card {
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  overflow: hidden;
  background: var(--td-bg-color-container);
  cursor: pointer;
  padding: 0;
  text-align: left;
  transition: box-shadow var(--app-motion-fast), transform var(--app-motion-fast);
  display: flex;
  flex-direction: column;
}
.ig-card:hover {
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  transform: translateY(-2px);
}
.ig-card-thumb {
  aspect-ratio: 4 / 3;
  background: #f3f3f3;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
.ig-card-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.ig-thumb-error {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  padding: 0 8px;
  text-align: center;
}
.ig-card-meta {
  padding: 8px 10px;
}
.ig-card-caption {
  font-size: var(--app-text-md);
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.ig-card-source {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  margin-top: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ig-pagination {
  margin-top: 16px;
  justify-content: center;
}

/* Viewer */
.ig-viewer-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  z-index: 2000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  box-sizing: border-box;
}
.ig-viewer {
  position: relative;
  display: grid;
  grid-template-columns: 1fr 340px;
  gap: 0;
  width: min(1100px, 96vw);
  height: min(80vh, 760px);
  background: var(--td-bg-color-container);
  border-radius: var(--app-radius-xl);
  overflow: hidden;
}
.ig-viewer-image {
  display: flex;
  align-items: center;
  justify-content: center;
  background: #1a1a1a;
  padding: 12px;
  min-width: 0;
}
.ig-viewer-image img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
.ig-viewer-image-error {
  color: #ddd;
}
.ig-viewer-info {
  padding: 20px;
  overflow: auto;
  border-left: 1px solid var(--td-component-border);
}
.ig-viewer-info h3 {
  font-size: var(--app-text-md);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--td-text-color-secondary);
  margin: 16px 0 8px;
}
.ig-viewer-info h3:first-child {
  margin-top: 0;
}
.ig-info-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 4px 0;
  font-size: var(--app-text-base);
}
.ig-info-key {
  color: var(--td-text-color-secondary);
}
.ig-info-val {
  font-weight: 500;
  text-align: right;
}
.ig-info-text {
  font-size: var(--app-text-base);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}
.ig-info-muted {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-base);
}
.ig-info-sub {
  display: block;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  margin-top: 2px;
  word-break: break-all;
}
.ig-viewer-close {
  position: absolute;
  top: 10px;
  right: 10px;
  z-index: 2;
  border: none;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ig-nav {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  z-index: 2;
  border: none;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ig-nav-prev {
  left: 12px;
}
.ig-nav-next {
  right: 352px;
}
</style>
