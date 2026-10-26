<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeFiles } from '@/api/knowledge-base'

// GallerySourcePicker is the "filter by source document" panel of the image
// gallery, rendered inside the toolbar popup next to the filter and sort
// panels: a keyword-searchable checkbox list of the knowledge base's
// documents, newest uploads first, loaded page by page as it scrolls. Like the
// filter panel, every tick applies at once (an empty selection clears the
// restriction).
//
// Overflow guards: a KB with a huge document count must neither flood the
// panel nor produce an unbounded gallery query. Without a keyword only the
// most recent RECENT_WINDOW documents are listed; a keyword hit list beyond
// SAFE_HITS cannot be select-all-ed (the user is asked to narrow the
// keyword), which also keeps one select-all fetch within the backend's
// page_size ceiling — the same 1000 the POST /images body cap enforces.
const props = defineProps<{
  kbId: string
  visible: boolean
  selected: string[]
}>()

const emit = defineEmits<{
  (e: 'update:selected', knowledgeIds: string[]): void
}>()

const NS = 'knowledgeEditor.wikiBrowser.gallery'
const { t } = useI18n()

const PAGE_SIZE = 30
const RECENT_WINDOW = 500
const SAFE_HITS = 1000
const SEARCH_DEBOUNCE_MS = 300
const LOAD_AHEAD_PX = 80

const keyword = ref('')
const page = ref(0)
const total = ref(0)
const loading = ref(false)
const bulkLoading = ref(false)
// Documents loaded so far for the current keyword.
const docs = ref<Array<{ id: string; file_name: string; created_at: string }>>([])
const listEl = ref<HTMLElement | null>(null)

const selectedSet = computed(() => new Set(props.selected))

// How many documents the listing actually offers: the raw total, capped to
// the window the overflow guards allow for the current search state.
const shownTotal = computed(() => {
  if (keyword.value.trim()) return Math.min(total.value, SAFE_HITS)
  return Math.min(total.value, RECENT_WINDOW)
})
const overflown = computed(() => total.value > shownTotal.value)
const hasMore = computed(() => docs.value.length < shownTotal.value)
// Select-all needs the full hit list in one fetch, so it is only offered
// while the current listing is fully enumerable.
const canSelectAll = computed(() => !overflown.value && shownTotal.value > 0)

const loadedAllSelected = computed(
  () => docs.value.length > 0 && docs.value.every((d) => selectedSet.value.has(d.id)),
)
const loadedSomeSelected = computed(() => docs.value.some((d) => selectedSet.value.has(d.id)))

// Bumped by every keyword change, so a response for an older search state
// (page fetch or select-all) is dropped.
let fetchToken = 0
async function loadPage(reset: boolean) {
  if (!props.kbId) return
  if (reset) {
    fetchToken++
    page.value = 0
    docs.value = []
    total.value = 0
    listEl.value?.scrollTo({ top: 0 })
  }
  const token = fetchToken
  const nextPage = page.value + 1
  loading.value = true
  try {
    const res: any = await listKnowledgeFiles(props.kbId, {
      page: nextPage,
      page_size: PAGE_SIZE,
      keyword: keyword.value.trim() || undefined,
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    if (token !== fetchToken) return
    const known = new Set(docs.value.map((d) => d.id))
    const items: any[] = res?.data ?? []
    for (const it of items) {
      const id = String(it.id)
      if (known.has(id)) continue
      docs.value.push({ id, file_name: it.file_name || it.title || id, created_at: it.created_at || '' })
    }
    page.value = nextPage
    total.value = items.length ? Number(res?.total) || 0 : docs.value.length
  } catch (_) {
    if (token === fetchToken && reset) total.value = 0
  } finally {
    if (token === fetchToken) loading.value = false
  }
}

function onListScroll() {
  const el = listEl.value
  if (!el || loading.value || !hasMore.value) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - LOAD_AHEAD_PX) void loadPage(false)
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
function cancelSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = undefined
}

watch(
  () => props.visible,
  (open) => {
    cancelSearch()
    if (!open) return
    keyword.value = ''
    void loadPage(true)
  },
  { immediate: true },
)

function onKeywordChange(value: string) {
  keyword.value = value
  cancelSearch()
  searchTimer = setTimeout(() => void loadPage(true), SEARCH_DEBOUNCE_MS)
}

function commit(next: Set<string>): boolean {
  if (next.size > SAFE_HITS) {
    MessagePlugin.warning(t(`${NS}.sourceFilterTooMany`))
    return false
  }
  emit('update:selected', Array.from(next))
  return true
}

function toggle(id: string, checked: boolean) {
  const next = new Set(props.selected)
  if (checked) next.add(id)
  else next.delete(id)
  commit(next)
}

// Header checkbox, across every hit of the current search (the guards above
// keep one fetch within the backend's page_size ceiling): drops the hits when
// all of them are already selected, adds them otherwise. Merged into the
// selection so earlier keywords survive.
async function onToggleAll() {
  if (!canSelectAll.value || bulkLoading.value) return
  const token = fetchToken
  bulkLoading.value = true
  try {
    const res: any = await listKnowledgeFiles(props.kbId, {
      page: 1,
      page_size: SAFE_HITS,
      keyword: keyword.value.trim() || undefined,
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    if (token !== fetchToken) return
    const ids: string[] = (res?.data ?? []).map((it: any) => String(it.id))
    if (!ids.length) return
    const next = new Set(props.selected)
    if (ids.every((id) => next.has(id))) {
      for (const id of ids) next.delete(id)
    } else {
      for (const id of ids) next.add(id)
    }
    commit(next)
  } catch (_) {
    // keep the selection untouched on failure
  } finally {
    bulkLoading.value = false
  }
}

function onClear() {
  emit('update:selected', [])
}

const pad = (n: number) => String(n).padStart(2, '0')

function formatDate(raw: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function formatTime(raw: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return `${formatDate(raw)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
</script>

<template>
  <section class="gsp-panel" :aria-label="t(`${NS}.sourceFilterTitle`)">
    <header class="gsp-panel__header">
      <strong>{{ t(`${NS}.sourceFilterTitle`) }}</strong>
      <button type="button" :disabled="!props.selected.length" @click="onClear">
        {{ t(`${NS}.sourceFilterClear`) }}
      </button>
    </header>
    <div class="gsp-panel__hint">{{ t(`${NS}.sourceFilterSubtitle`) }}</div>

    <t-input
      :value="keyword"
      clearable
      :placeholder="t(`${NS}.sourceFilterSearch`)"
      :aria-label="t(`${NS}.sourceFilterSearch`)"
      class="gsp-search"
      @change="(v: string) => onKeywordChange(String(v ?? ''))"
    >
      <template #prefix-icon><t-icon name="search" size="16px" /></template>
    </t-input>

    <div v-if="overflown" class="gsp-overflow-hint" role="status">
      <t-icon name="info-circle" size="14px" />
      <span>
        {{
          keyword.trim()
            ? t(`${NS}.sourceFilterOverflowHits`, { total: total, cap: shownTotal })
            : t(`${NS}.sourceFilterOverflowRecent`, { cap: shownTotal })
        }}
      </span>
    </div>

    <div class="gsp-bar">
      <t-checkbox
        :checked="loadedAllSelected"
        :indeterminate="loadedSomeSelected && !loadedAllSelected"
        :disabled="!canSelectAll || bulkLoading"
        :title="canSelectAll ? t(`${NS}.sourceFilterSelectAll`) : undefined"
        @change="onToggleAll"
      >
        {{ t('common.selectAll') }}
      </t-checkbox>
      <span class="gsp-bar__count">{{ t(`${NS}.sourceFilterSelected`, { count: props.selected.length }) }}</span>
    </div>

    <div ref="listEl" class="gsp-list" @scroll.passive="onListScroll">
      <div
        v-for="d in docs"
        :key="d.id"
        class="gsp-item"
        :class="{ 'is-selected': selectedSet.has(d.id) }"
        :title="`${d.file_name}${d.created_at ? ' · ' + formatTime(d.created_at) : ''}`"
        @click="toggle(d.id, !selectedSet.has(d.id))"
      >
        <span class="gsp-item__check" @click.stop>
          <t-checkbox
            :checked="selectedSet.has(d.id)"
            @change="(checked: boolean) => toggle(d.id, checked)"
          />
        </span>
        <span class="gsp-item__name">{{ d.file_name }}</span>
        <span class="gsp-item__time">{{ formatDate(d.created_at) }}</span>
      </div>
      <div v-if="loading || bulkLoading" class="gsp-list__status">
        <t-loading size="small" />
      </div>
      <div v-else-if="!docs.length" class="gsp-list__status">
        {{ t(`${NS}.sourceFilterEmpty`) }}
      </div>
    </div>
  </section>
</template>

<style scoped lang="less">
// Same frame and type scale as the gallery's filter panel.
.gsp-panel {
  display: flex;
  flex-direction: column;
  width: 360px;
  max-width: calc(100vw - 32px);
  padding: 16px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;

    strong {
      font-size: var(--app-text-base);
      font-weight: 600;
    }

    button {
      padding: 0;
      border: 0;
      background: transparent;
      color: var(--td-text-color-secondary);
      font: inherit;
      font-size: var(--app-text-sm);
      cursor: pointer;

      &:hover:not(:disabled) { color: var(--td-brand-color); }
      &:disabled { color: var(--td-text-color-disabled); cursor: default; }
    }
  }

  &__hint {
    margin: 2px 0 12px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-xs);
    line-height: 17px;
  }
}

.gsp-search :deep(.t-input) {
  border-color: var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  font-size: var(--app-text-md);
}

.gsp-overflow-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 8px;
  color: var(--td-warning-color);
  font-size: var(--app-text-xs);
  line-height: 17px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}

.gsp-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 12px;
  padding: 0 8px 8px;
  border-bottom: 1px solid var(--td-component-stroke);

  &__count {
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
}

.gsp-list {
  max-height: 288px;
  min-height: 96px;
  margin: 4px -8px 0;
  padding: 0 8px;
  overflow-y: auto;

  &__status {
    display: flex;
    justify-content: center;
    padding: 16px 0;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
  }
}

.gsp-item {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 8px;
  border-radius: var(--app-radius-sm);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;

  &:hover { background: var(--td-bg-color-secondarycontainer); }

  &__check {
    display: flex;
    flex-shrink: 0;
  }

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__time {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-xs);
    font-variant-numeric: tabular-nums;
  }

  &.is-selected .gsp-item__name { color: var(--td-brand-color); }
}
</style>
