<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  listGalleryImages,
  type ImageAsset,
  type ImageListParams,
  type ImageSortBy,
} from '@/api/image-gallery'
import {
  fetchImageAttrSchema,
  FALLBACK_IMAGE_ATTR_SCHEMA,
  type ImageAttrSchema,
  type ImageAttrSpec,
} from '@/api/knowledge-base'
import {
  imageAttrDisplay,
  type ImageAttrDisplay,
  type ImageAttrValueDisplay,
} from '@/utils/imageAttrDisplay'

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
const sortBy = ref<ImageSortBy>('created_at')
const sortOrder = ref<'asc' | 'desc'>('desc')
const enabledFilter = ref<'all' | 'enabled' | 'disabled'>('all')
// Attribute multi-select: attribute name -> selected allowed values (OR within).
const attrSelections = ref<Record<string, string[]>>({})

// Schema-driven filter options.
const schema = ref<ImageAttrSchema>(FALLBACK_IMAGE_ATTR_SCHEMA)
const schemaDisplays = ref<ImageAttrDisplay[]>([])

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
// Attribute display helpers (uses the schema registry for human wording)
// ---------------------------------------------------------------------------
function buildSchemaDisplays(specs: ImageAttrSpec[]) {
  schemaDisplays.value = specs.map((spec) => imageAttrDisplay(spec, t, te))
}

/** Render one observed attribute value in words, falling back to the raw value. */
function displayAttrValue(name: string, raw: unknown): string {
  const attr = schemaDisplays.value.find((a) => a.name === name)
  if (!attr) return String(raw)
  let normalized: string
  if (typeof raw === 'boolean') normalized = String(raw).toLowerCase()
  else if (typeof raw === 'number') normalized = String(raw)
  else normalized = String(raw)
  const match: ImageAttrValueDisplay | undefined = attr.values.find(
    (v) => v.value === normalized,
  )
  return match ? match.label : normalized
}

function attrLabel(name: string): string {
  const attr = schemaDisplays.value.find((a) => a.name === name)
  return attr ? attr.label : name
}

const currentAttrs = computed(() => {
  if (!current.value) return [] as Array<{ name: string; label: string; value: string }>
  return Object.entries(current.value.attrs).map(([name, raw]) => ({
    name,
    label: attrLabel(name),
    value: displayAttrValue(name, raw),
  }))
})

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------
function buildParams(): ImageListParams {
  const attrFilters: Record<string, string[]> = {}
  for (const [name, values] of Object.entries(attrSelections.value)) {
    if (values && values.length) attrFilters[name] = values
  }
  const params: ImageListParams = {
    keyword: keyword.value.trim() || undefined,
    sortBy: sortBy.value,
    sortOrder: sortOrder.value,
    attrFilters: Object.keys(attrFilters).length ? attrFilters : undefined,
    page: page.value,
    pageSize: pageSize.value,
  }
  if (enabledFilter.value === 'enabled') params.isEnabled = true
  else if (enabledFilter.value === 'disabled') params.isEnabled = false
  return params
}

async function reload() {
  if (!props.knowledgeBaseId) return
  loading.value = true
  error.value = ''
  try {
    const res = await listGalleryImages(props.knowledgeBaseId, buildParams())
    items.value = res.items
    total.value = res.total
    if (viewerOpen.value && viewerIndex.value >= res.items.length) closeViewer()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function resetPageAndReload() {
  page.value = 1
  reload()
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

function onAttrGroupChange(attrName: string, values: Array<string | number | boolean>) {
  attrSelections.value = {
    ...attrSelections.value,
    [attrName]: values.map((v) => String(v)),
  }
  resetPageAndReload()
}

function clearFilters() {
  keyword.value = ''
  enabledFilter.value = 'all'
  attrSelections.value = {}
  resetPageAndReload()
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
onMounted(async () => {
  try {
    schema.value = await fetchImageAttrSchema(props.knowledgeBaseId)
  } catch {
    schema.value = FALLBACK_IMAGE_ATTR_SCHEMA
  }
  buildSchemaDisplays(schema.value.attributes)
  await reload()
})
</script>

<template>
  <div class="image-gallery" @keydown="onViewerKey">
    <div class="ig-layout">
      <!-- Filter sidebar -->
      <aside class="ig-filters">
        <div class="ig-filters-title">{{ t('knowledgeEditor.wikiBrowser.gallery.filtersTitle') }}</div>

        <div class="ig-filter-group">
          <label class="ig-filter-label">{{ t('knowledgeEditor.wikiBrowser.gallery.statusLabel') }}</label>
          <t-radio-group v-model="enabledFilter" variant="default-filled" @change="resetPageAndReload">
            <t-radio value="all">{{ t('knowledgeEditor.wikiBrowser.gallery.statusAll') }}</t-radio>
            <t-radio value="enabled">{{ t('knowledgeEditor.wikiBrowser.gallery.statusEnabled') }}</t-radio>
            <t-radio value="disabled">{{ t('knowledgeEditor.wikiBrowser.gallery.statusDisabled') }}</t-radio>
          </t-radio-group>
        </div>

        <div v-if="schemaDisplays.length" class="ig-filter-group">
          <label class="ig-filter-label">{{ t('knowledgeEditor.wikiBrowser.gallery.attrSection') }}</label>
          <div v-for="attr in schemaDisplays" :key="attr.name" class="ig-attr-filter">
            <div class="ig-attr-name" :title="attr.description">{{ attr.label }}</div>
            <t-checkbox-group
              :value="attrSelections[attr.name] || []"
              @change="(vals: Array<string | number | boolean>) => onAttrGroupChange(attr.name, vals)"
            >
              <t-checkbox
                v-for="v in attr.values"
                :key="v.value"
                :value="v.value"
              >
                {{ v.label }}
              </t-checkbox>
            </t-checkbox-group>
          </div>
        </div>
        <div v-else class="ig-no-attrs">{{ t('knowledgeEditor.wikiBrowser.gallery.noAttrs') }}</div>

        <t-button theme="default" variant="text" class="ig-clear" @click="clearFilters">
          {{ t('knowledgeEditor.wikiBrowser.gallery.clearFilters') }}
        </t-button>
      </aside>

      <!-- Main content -->
      <section class="ig-main">
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

          <t-select v-model="sortBy" class="ig-sort" @change="resetPageAndReload">
            <t-option value="created_at" :label="t('knowledgeEditor.wikiBrowser.gallery.sortCreatedAt')" />
            <t-option value="updated_at" :label="t('knowledgeEditor.wikiBrowser.gallery.sortUpdatedAt')" />
            <t-option value="caption" :label="t('knowledgeEditor.wikiBrowser.gallery.sortCaption')" />
          </t-select>

          <t-button theme="default" variant="outline" @click="sortOrder = sortOrder === 'asc' ? 'desc' : 'asc'; resetPageAndReload()">
            <t-icon :name="sortOrder === 'asc' ? 'arrow-up' : 'arrow-down'" />
            {{ sortOrder === 'asc' ? t('knowledgeEditor.wikiBrowser.gallery.orderAsc') : t('knowledgeEditor.wikiBrowser.gallery.orderDesc') }}
          </t-button>

          <span class="ig-count">{{ t('knowledgeEditor.wikiBrowser.gallery.count', { count: total }) }}</span>
        </div>

        <t-loading :loading="loading" class="ig-loading-area">
          <div v-if="error" class="ig-error">{{ error }}</div>

          <div v-else-if="!items.length" class="ig-empty">
            {{ keyword || enabledFilter !== 'all' || Object.values(attrSelections).some((v) => v.length)
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
                <img :src="img.url" :alt="img.caption" loading="lazy" />
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
          <img v-if="!imageFailed" :src="current.url" :alt="current.caption" @error="imageFailed = true" />
          <div v-else class="ig-viewer-image-error">{{ t('knowledgeEditor.wikiBrowser.gallery.imageLoadError') }}</div>
        </div>

        <button class="ig-nav ig-nav-next" :title="t('knowledgeEditor.wikiBrowser.gallery.next')" @click="nextImage">
          <t-icon name="chevron-right" />
        </button>

        <aside class="ig-viewer-info">
          <h3>{{ t('knowledgeEditor.wikiBrowser.gallery.attributes') }}</h3>
          <div v-if="currentAttrs.length">
            <div v-for="a in currentAttrs" :key="a.name" class="ig-info-row">
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
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 16px;
  box-sizing: border-box;
}

.ig-layout {
  display: grid;
  grid-template-columns: 240px 1fr;
  gap: 16px;
  flex: 1;
  min-height: 0;
}

/* Filters */
.ig-filters {
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 8px;
  padding: 14px;
  align-self: start;
  max-height: 100%;
  overflow: auto;
  background: var(--td-bg-color-container, #fff);
}
.ig-filters-title {
  font-weight: 600;
  margin-bottom: 12px;
  font-size: 14px;
}
.ig-filter-group {
  margin-bottom: 18px;
}
.ig-filter-label {
  display: block;
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
  margin-bottom: 8px;
}
.ig-attr-filter {
  margin-bottom: 12px;
}
.ig-attr-name {
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 4px;
}
.ig-no-attrs,
.ig-clear {
  font-size: 13px;
}
.ig-clear {
  margin-top: 4px;
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
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
  white-space: nowrap;
}
.ig-loading-area {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.ig-error {
  color: var(--td-error-color, #e34d59);
  padding: 16px 0;
}
.ig-empty {
  color: var(--td-text-color-placeholder, #999);
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
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 8px;
  overflow: hidden;
  background: var(--td-bg-color-container, #fff);
  cursor: pointer;
  padding: 0;
  text-align: left;
  transition: box-shadow 0.15s, transform 0.15s;
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
.ig-card-meta {
  padding: 8px 10px;
}
.ig-card-caption {
  font-size: 13px;
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.ig-card-source {
  font-size: 12px;
  color: var(--td-text-color-secondary, #666);
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
  background: var(--td-bg-color-container, #fff);
  border-radius: 12px;
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
  border-left: 1px solid var(--td-component-border, #e7e7e7);
}
.ig-viewer-info h3 {
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--td-text-color-secondary, #666);
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
  font-size: 14px;
}
.ig-info-key {
  color: var(--td-text-color-secondary, #666);
}
.ig-info-val {
  font-weight: 500;
  text-align: right;
}
.ig-info-text {
  font-size: 14px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}
.ig-info-muted {
  color: var(--td-text-color-placeholder, #999);
  font-size: 14px;
}
.ig-info-sub {
  display: block;
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
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
