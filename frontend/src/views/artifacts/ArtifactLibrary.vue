<template>
  <div class="artifact-library">
    <div class="header" style="--wails-draggable: drag">
      <div class="header-title" style="--wails-draggable: drag">
        <h2 style="--wails-draggable: drag">
          <ResourceIcon type="artifact" :size="24" />
          {{ $t('artifactLibrary.title') }}
        </h2>
        <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('artifactLibrary.subtitle') }}</p>
      </div>
    </div>

    <div class="toolbar">
      <div class="category-tabs" role="tablist" :aria-label="$t('artifactLibrary.typeFilter')">
        <button
          v-for="c in ARTIFACT_CATEGORIES"
          :key="c"
          type="button"
          role="tab"
          :aria-selected="category === c"
          @click="setCategory(c)"
        >
          {{ $t(`artifactLibrary.categories.${c}`) }}
        </button>
      </div>
      <t-input
        v-model="keyword"
        class="search-input"
        :placeholder="$t('artifactLibrary.searchPlaceholder')"
        :aria-label="$t('artifactLibrary.searchPlaceholder')"
        clearable
      >
        <template #prefix-icon><t-icon name="search" size="16px" /></template>
      </t-input>
    </div>

    <div ref="scrollRoot" class="library-main">
      <div v-if="loading && !items.length" class="artifact-rows">
        <div v-for="n in 6" :key="'skel-' + n" class="artifact-row is-skeleton">
          <t-skeleton animation="gradient" :row-col="[[{ width: '32px', height: '38px', type: 'rect' },
            { width: '40%', height: '14px' }]]" />
        </div>
      </div>

      <EmptyState
        v-else-if="loadError && !items.length"
        icon="error-circle"
        :title="$t('artifactLibrary.loadFailed')"
      >
        <t-button variant="outline" @click="reload">{{ $t('artifactLibrary.retry') }}</t-button>
      </EmptyState>

      <EmptyState
        v-else-if="!items.length && isFiltered"
        icon="search"
        :title="$t('artifactLibrary.noMatches.title')"
        :description="$t('artifactLibrary.noMatches.description')"
      >
        <t-button variant="outline" @click="clearFilters">{{ $t('artifactLibrary.clearFilters') }}</t-button>
      </EmptyState>

      <EmptyState
        v-else-if="!items.length"
        icon="folder-open"
        :title="$t('artifactLibrary.empty.title')"
        :description="$t('artifactLibrary.empty.description')"
      />

      <template v-else>
        <div class="result-count">{{ $t('artifactLibrary.total', { count: total }) }}</div>
        <section v-for="section in sections" :key="section.group" class="date-section">
          <h3 class="date-heading">{{ $t(`artifactLibrary.groups.${section.group}`) }}</h3>
          <ul class="artifact-rows">
            <li
              v-for="item in section.items"
              :key="`${item.message_id}:${item.index}`"
              class="artifact-row"
              @click="openPreview(item)"
            >
              <button type="button" class="artifact-open" :title="$t('artifactLibrary.preview')">
                <ArtifactFileIcon :file-name="item.file_name" />
                <span class="artifact-body">
                  <span class="artifact-name" :title="item.file_name">{{ item.file_name }}</span>
                  <span class="artifact-meta">
                    <span>{{ formatArtifactSize(item.file_size) }}</span>
                    <span class="meta-sep">·</span>
                    <span>{{ formatArtifactDateTime(item.created_at) }}</span>
                    <template v-if="item.version_count > 1">
                      <span class="meta-sep">·</span>
                      <span class="version-badge">
                        {{ $t('artifactLibrary.versions', { count: item.version_count }) }}
                      </span>
                    </template>
                  </span>
                </span>
              </button>
              <button
                type="button"
                class="session-link"
                :title="$t('artifactLibrary.openSession')"
                @click.stop="openSession(item)"
              >
                <t-icon name="chat" size="14px" />
                <span>{{ item.session_title || $t('artifactLibrary.untitledSession') }}</span>
              </button>
              <t-button
                class="row-action"
                variant="text"
                shape="square"
                size="small"
                :title="$t('artifactLibrary.download')"
                :aria-label="$t('artifactLibrary.download')"
                :loading="isDownloading(item)"
                @click.stop="handleDownload(item)"
              >
                <template #icon><t-icon name="download" size="16px" /></template>
              </t-button>
            </li>
          </ul>
        </section>

        <div ref="sentinel" class="list-footer">
          <t-loading v-if="loading" size="small" />
          <t-button v-else-if="hasMore" variant="text" @click="loadMore">
            {{ $t('artifactLibrary.loadMore') }}
          </t-button>
        </div>
      </template>
    </div>

    <t-drawer
      v-model:visible="previewVisible"
      class="artifact-library-preview"
      placement="right"
      size="min(880px, 92vw)"
      attach="body"
      :footer="false"
      :close-on-overlay-click="true"
      :close-on-esc-keydown="true"
      @closed="previewItem = null"
    >
      <template #header>
        <div v-if="previewItem" class="preview-header">
          <ArtifactFileIcon class="preview-header-icon" :file-name="previewItem.file_name" />
          <div class="preview-header-text">
            <div class="preview-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
            <button type="button" class="session-link session-link--inline" @click="openSession(previewItem)">
              <t-icon name="chat" size="12px" />
              <span>{{ previewItem.session_title || $t('artifactLibrary.untitledSession') }}</span>
            </button>
          </div>
          <div ref="previewActions" class="preview-actions" />
          <t-button
            class="row-action"
            variant="text"
            shape="square"
            size="small"
            :title="$t('artifactLibrary.download')"
            :aria-label="$t('artifactLibrary.download')"
            :loading="isDownloading(previewItem)"
            @click="handleDownload(previewItem)"
          >
            <template #icon><t-icon name="download" size="16px" /></template>
          </t-button>
          <t-button
            class="row-action"
            variant="text"
            shape="square"
            size="small"
            :title="$t('common.close')"
            :aria-label="$t('common.close')"
            @click="previewVisible = false"
          >
            <template #icon><t-icon name="close" size="16px" /></template>
          </t-button>
        </div>
      </template>
      <div v-if="previewItem" class="preview-body">
        <DocumentPreview
          :key="`${previewItem.message_id}:${previewItem.index}`"
          :toolbar-target="previewActions"
          :session-id="previewItem.session_id"
          :message-id="previewItem.message_id"
          :artifact-index="previewItem.index"
          :file-type="previewFileType"
          :file-name="previewItem.file_name"
          :active="previewVisible"
          fill-height
        />
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact } from '@/api/chat'
import { listArtifactLibrary, type ArtifactLibraryItem } from '@/api/artifacts'
import {
  ARTIFACT_CATEGORIES,
  artifactCategoryExtensions,
  groupArtifactsByDate,
  parseArtifactCategory,
  type ArtifactCategory,
} from '@/utils/artifactLibrary'
import { formatArtifactDateTime, formatArtifactSize } from '@/utils/sessionArtifacts'
import { resolveFilePreviewExt } from '@/utils/filePreview'
import EmptyState from '@/components/EmptyState.vue'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'
import DocumentPreview from '@/components/document-preview.vue'
import ArtifactFileIcon from '@/views/chat/components/ArtifactFileIcon.vue'

const PAGE_SIZE = 30
const SEARCH_DEBOUNCE_MS = 300

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const category = ref<ArtifactCategory>(parseArtifactCategory(route.query.type))
const keyword = ref(typeof route.query.q === 'string' ? route.query.q : '')
const items = ref<ArtifactLibraryItem[]>([])
const total = ref(0)
const page = ref(0)
const loading = ref(false)
const loadError = ref(false)
const downloading = reactive<Record<string, boolean>>({})
const previewItem = ref<ArtifactLibraryItem | null>(null)
const previewVisible = ref(false)
const previewActions = ref<HTMLElement | null>(null)
const scrollRoot = ref<HTMLElement | null>(null)
const sentinel = ref<HTMLElement | null>(null)

const sections = computed(() => groupArtifactsByDate(items.value))
const hasMore = computed(() => items.value.length < total.value)
const isFiltered = computed(() => category.value !== 'all' || keyword.value.trim() !== '')
const previewFileType = computed(() => {
  const item = previewItem.value
  return item ? resolveFilePreviewExt(item.file_name, item.file_type) : ''
})

// Every fetch takes a ticket; a response whose ticket is stale (the filters
// changed while it was in flight) is dropped.
let ticket = 0

async function fetchPage(nextPage: number) {
  const mine = ++ticket
  loading.value = true
  loadError.value = false
  try {
    const res = await listArtifactLibrary({
      keyword: keyword.value,
      fileTypes: artifactCategoryExtensions(category.value),
      page: nextPage,
      pageSize: PAGE_SIZE,
    })
    if (mine !== ticket) return
    const rows = Array.isArray(res?.data) ? res.data : []
    items.value = nextPage === 1 ? rows : [...items.value, ...rows]
    total.value = typeof res?.total === 'number' ? res.total : items.value.length
    page.value = nextPage
  } catch (err) {
    if (mine !== ticket) return
    console.error('[ArtifactLibrary] load failed:', err)
    loadError.value = true
    if (nextPage > 1) MessagePlugin.error(t('artifactLibrary.loadFailed'))
  } finally {
    if (mine === ticket) loading.value = false
  }
}

function reload() {
  items.value = []
  total.value = 0
  page.value = 0
  void fetchPage(1)
}

function loadMore() {
  if (loading.value || !hasMore.value) return
  void fetchPage(page.value + 1)
}

function syncQuery() {
  const query = { ...route.query }
  if (category.value === 'all') delete query.type
  else query.type = category.value
  const q = keyword.value.trim()
  if (q) query.q = q
  else delete query.q
  void router.replace({ query })
}

function setCategory(next: ArtifactCategory) {
  if (category.value === next) return
  category.value = next
}

function clearFilters() {
  category.value = 'all'
  keyword.value = ''
}

let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(keyword, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = null
    syncQuery()
    reload()
  }, SEARCH_DEBOUNCE_MS)
})
watch(category, () => {
  syncQuery()
  reload()
})

function openPreview(item: ArtifactLibraryItem) {
  previewItem.value = item
  previewVisible.value = true
}

function openSession(item: ArtifactLibraryItem) {
  previewVisible.value = false
  void router.push(`/platform/chat/${item.session_id}`)
}

function downloadKey(item: ArtifactLibraryItem) {
  return `${item.message_id}:${item.index}`
}

function isDownloading(item: ArtifactLibraryItem) {
  return !!downloading[downloadKey(item)]
}

async function handleDownload(item: ArtifactLibraryItem) {
  const key = downloadKey(item)
  if (downloading[key]) return
  downloading[key] = true
  try {
    const blob = await downloadArtifact(item.session_id, item.message_id, item.index)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = item.file_name || 'artifact'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    console.error('[ArtifactLibrary] download failed:', err)
    MessagePlugin.error(t('artifactLibrary.downloadFailed'))
  } finally {
    downloading[key] = false
  }
}

// Load the next page as the footer scrolls into view; the footer button
// covers browsers without IntersectionObserver and failed auto-loads.
let observer: IntersectionObserver | null = null
watch(sentinel, (el, prev) => {
  if (prev) observer?.unobserve(prev)
  if (el) observer?.observe(el)
})

onMounted(() => {
  if (typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting) && !loadError.value) loadMore()
      },
      { root: scrollRoot.value, rootMargin: '200px' },
    )
    if (sentinel.value) observer.observe(sentinel.value)
  }
  void fetchPage(1)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  if (searchTimer) clearTimeout(searchTimer)
  ticket++
})
</script>

<style scoped lang="less">
@import '@/components/css/artifact-filter-tabs.less';

.artifact-library {
  flex: 1;
  min-width: 0;
  height: 100%;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  margin: 0 16px 0 0;
  padding: 20px 28px 0;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  flex-shrink: 0;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 32px;
  }
}

.header-subtitle {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 20px;
}

.toolbar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--app-space-3);
  padding-bottom: var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
}

.category-tabs {
  .artifact-filter-tabs();
}

.search-input {
  width: 260px;
  max-width: 100%;
}

.library-main {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  display: flex;
  flex-direction: column;
  padding: var(--app-space-3) 0 var(--app-space-6);
}

.result-count {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  padding: 0 var(--app-space-2) var(--app-space-1);
}

.date-section + .date-section {
  margin-top: var(--app-space-3);
}

.date-heading {
  margin: 0;
  padding: var(--app-space-2) var(--app-space-2) var(--app-space-1);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
}

.artifact-rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

.artifact-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: 10px var(--app-space-2);
  border-radius: var(--app-radius-md);
  cursor: pointer;
  transition: background var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-skeleton {
    cursor: default;

    &:hover {
      background: transparent;
    }
  }
}

.artifact-open {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 4px;
    border-radius: var(--app-radius-xs);
  }
}

.artifact-body {
  flex: 1;
  min-width: 0;
}

.artifact-name {
  display: block;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.artifact-meta {
  margin-top: 2px;
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.3;
  white-space: nowrap;
}

.meta-sep {
  opacity: 0.6;
}

.version-badge {
  color: var(--td-text-color-secondary);
}

.session-link {
  flex: 0 1 240px;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: 4px 8px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  span {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  .t-icon {
    flex-shrink: 0;
  }

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }

  &--inline {
    flex: none;
    max-width: 100%;
    padding: 0;
    margin-top: 2px;

    &:hover {
      background: transparent;
    }
  }
}

.row-action.t-button {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);

  &:not(.t-is-disabled):not(.t-is-loading):hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 10%, var(--td-bg-color-container));
    color: var(--td-text-color-primary);
  }

  :deep(.t-button__icon) {
    margin: 0;
  }
}

.list-footer {
  display: flex;
  justify-content: center;
  min-height: 40px;
  padding-top: var(--app-space-3);
}

@media (max-width: 720px) {
  .artifact-library {
    margin: 0;
    padding: var(--app-space-4) var(--app-space-4) 0;
  }

  .search-input {
    width: 100%;
  }

  .session-link:not(.session-link--inline) {
    display: none;
  }
}
</style>

<style lang="less">
.artifact-library-preview {
  .t-drawer__header {
    padding: 10px 16px;
  }

  .t-drawer__body {
    padding: 8px;
    display: flex;
    flex-direction: column;
  }

  .preview-header {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    width: 100%;
    min-width: 0;
  }

  .preview-header-icon {
    width: 26px;
    height: 32px;
  }

  .preview-header-text {
    flex: 1;
    min-width: 0;
  }

  .preview-header-title {
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    font-weight: 600;
    line-height: 1.4;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  .preview-actions {
    flex-shrink: 0;
  }

  .preview-body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
}
</style>
