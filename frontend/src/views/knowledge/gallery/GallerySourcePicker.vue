<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeFiles } from '@/api/knowledge-base'

// GallerySourcePicker is the "filter by source document" dialog of the image
// gallery: a paginated, keyword-searchable, sortable checkbox list of the
// knowledge base's documents. Confirming hands the selected ids to the
// gallery (an empty selection clears the restriction); the selection starts
// from what is already active so reopening shows the state the user last
// committed.
//
// Overflow guards: a KB with a huge document count must neither flood the
// dialog nor produce an unbounded gallery query. Without a keyword only the
// most recent RECENT_WINDOW documents are shown; a keyword hit list beyond
// SAFE_HITS cannot be select-all-ed (the user is asked to narrow the
// keyword), which also keeps one select-all fetch within the backend's
// page_size ceiling — the same 1000 the POST /images body cap enforces.
const props = defineProps<{
  kbId: string
  visible: boolean
  selected: string[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'confirm', knowledgeIds: string[]): void
}>()

const NS = 'knowledgeEditor.wikiBrowser.gallery'
const { t } = useI18n()

const PAGE_SIZE = 20
const RECENT_WINDOW = 500
const SAFE_HITS = 1000
const SEARCH_DEBOUNCE_MS = 300

const keyword = ref('')
const page = ref(1)
const total = ref(0)
const loading = ref(false)
const bulkLoading = ref(false)
// Sort of the document list; newest uploads first by default so recently
// added documents are at hand.
const sortBy = ref<'created_at' | 'file_name'>('created_at')
const sortDesc = ref(true)
// Documents of the current page.
const docs = ref<Array<{ id: string; file_name: string; created_at: string }>>([])
// Draft selection while the dialog is open; committed on confirm. Search,
// sort and paging only re-render the list and never touch the draft, so a
// keyword-A select-all survives a later keyword-B one (union, not replace).
const draft = ref<Set<string>>(new Set())

// How many documents the listing actually offers: the raw total, capped to
// the window the overflow guards allow for the current search state.
const shownTotal = computed(() => {
  if (keyword.value.trim()) return Math.min(total.value, SAFE_HITS)
  return Math.min(total.value, RECENT_WINDOW)
})
const overflown = computed(() => total.value > shownTotal.value)
// Select-all needs the full hit list in one fetch, so it is only offered
// while the current listing is fully enumerable.
const canSelectAll = computed(() => !overflown.value && shownTotal.value > 0)

const pageAllSelected = computed(
  () => docs.value.length > 0 && docs.value.every((d) => draft.value.has(d.id)),
)
const pageSomeSelected = computed(() => docs.value.some((d) => draft.value.has(d.id)))

// Confirming is a no-op only when nothing was committed and nothing is picked.
const canConfirm = computed(() => draft.value.size > 0 || props.selected.length > 0)

// Bumped by every listing change, so a response for an older search state
// (page fetch or select-all) is dropped.
let fetchToken = 0
async function loadDocs() {
  if (!props.kbId) return
  const token = ++fetchToken
  loading.value = true
  try {
    const res: any = await listKnowledgeFiles(props.kbId, {
      page: page.value,
      page_size: PAGE_SIZE,
      keyword: keyword.value.trim() || undefined,
      sort_by: sortBy.value,
      sort_order: sortDesc.value ? 'desc' : 'asc',
    })
    if (token !== fetchToken) return
    const items: any[] = res?.data ?? []
    docs.value = items.map((it) => ({
      id: String(it.id),
      file_name: it.file_name || it.title || it.id,
      created_at: it.created_at || '',
    }))
    total.value = Number(res?.total) || 0
  } catch (_) {
    if (token === fetchToken) {
      docs.value = []
      total.value = 0
    }
  } finally {
    if (token === fetchToken) loading.value = false
  }
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
    draft.value = new Set(props.selected)
    keyword.value = ''
    page.value = 1
    sortBy.value = 'created_at'
    sortDesc.value = true
    void loadDocs()
  },
)

function onKeywordChange(value: string) {
  keyword.value = value
  cancelSearch()
  searchTimer = setTimeout(() => {
    page.value = 1
    void loadDocs()
  }, SEARCH_DEBOUNCE_MS)
}

// TDesign's pagination change event hands over an info object, not a number.
function onPageChange(info: { current: number; previous: number; pageSize: number }) {
  page.value = info.current
  void loadDocs()
}

function toggleSort(field: 'created_at' | 'file_name') {
  if (sortBy.value === field) {
    sortDesc.value = !sortDesc.value
  } else {
    sortBy.value = field
    // Newest first for upload time, A→Z for names.
    sortDesc.value = field === 'created_at'
  }
  page.value = 1
  void loadDocs()
}

function toggle(id: string, checked: boolean) {
  const next = new Set(draft.value)
  if (checked) next.add(id)
  else next.delete(id)
  draft.value = next
}

// Header checkbox, across every hit of the current search (the guards above
// keep one fetch within the backend's page_size ceiling): drops the hits when
// all of them are already selected, adds them otherwise. Merged into the
// draft so earlier keywords survive.
async function onToggleAll() {
  if (!canSelectAll.value || bulkLoading.value) return
  const token = fetchToken
  bulkLoading.value = true
  try {
    const res: any = await listKnowledgeFiles(props.kbId, {
      page: 1,
      page_size: SAFE_HITS,
      keyword: keyword.value.trim() || undefined,
      sort_by: sortBy.value,
      sort_order: sortDesc.value ? 'desc' : 'asc',
    })
    if (token !== fetchToken) return
    const ids: string[] = (res?.data ?? []).map((it: any) => String(it.id))
    if (!ids.length) return
    const next = new Set(draft.value)
    if (ids.every((id) => next.has(id))) {
      for (const id of ids) next.delete(id)
    } else {
      for (const id of ids) next.add(id)
    }
    draft.value = next
  } catch (_) {
    // keep the draft untouched on failure
  } finally {
    bulkLoading.value = false
  }
}

function close() {
  emit('update:visible', false)
}

function onConfirm() {
  if (draft.value.size > SAFE_HITS) {
    MessagePlugin.warning(t(`${NS}.sourceFilterTooMany`))
    return
  }
  emit('confirm', Array.from(draft.value))
  close()
}

function onClearDraft() {
  draft.value = new Set()
}

function formatTime(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
</script>

<template>
  <t-dialog
    :visible="props.visible"
    :footer="false"
    width="640px"
    dialog-class-name="gallery-source-dialog"
    :close-on-overlay-click="false"
    @close="close"
  >
    <template #header>
      <div class="gsp-heading">
        <div class="gsp-heading__row">
          <t-icon name="file" size="16px" class="gsp-heading__icon" aria-hidden="true" />
          <span class="gsp-heading__title">{{ t(`${NS}.sourceFilterTitle`) }}</span>
        </div>
        <p class="gsp-heading__subtitle">{{ t(`${NS}.sourceFilterSubtitle`) }}</p>
      </div>
    </template>

    <div class="gsp-body">
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

      <t-loading :loading="loading || bulkLoading" size="small" show-overlay>
        <div class="gsp-table">
          <div class="gsp-row gsp-head">
            <div class="gsp-col-check">
              <t-checkbox
                :checked="pageAllSelected"
                :indeterminate="pageSomeSelected && !pageAllSelected"
                :disabled="!canSelectAll"
                :title="canSelectAll ? t(`${NS}.sourceFilterSelectAll`) : undefined"
                @change="onToggleAll"
              />
            </div>
            <button
              type="button"
              class="gsp-head-cell"
              :class="{ 'is-active': sortBy === 'file_name' }"
              @click="toggleSort('file_name')"
            >
              <span>{{ t(`${NS}.sourceFilterColName`) }}</span>
              <t-icon v-if="sortBy === 'file_name'" :name="sortDesc ? 'arrow-down' : 'arrow-up'" size="14px" />
            </button>
            <button
              type="button"
              class="gsp-head-cell"
              :class="{ 'is-active': sortBy === 'created_at' }"
              @click="toggleSort('created_at')"
            >
              <span>{{ t(`${NS}.sourceFilterColTime`) }}</span>
              <t-icon v-if="sortBy === 'created_at'" :name="sortDesc ? 'arrow-down' : 'arrow-up'" size="14px" />
            </button>
          </div>

          <div class="gsp-list">
            <div
              v-for="d in docs"
              :key="d.id"
              class="gsp-row gsp-item"
              :class="{ 'is-selected': draft.has(d.id) }"
              @click="toggle(d.id, !draft.has(d.id))"
            >
              <div class="gsp-col-check" @click.stop>
                <t-checkbox :checked="draft.has(d.id)" @change="(checked: boolean) => toggle(d.id, checked)" />
              </div>
              <div class="gsp-col-name" :title="d.file_name">{{ d.file_name }}</div>
              <div class="gsp-col-time">{{ formatTime(d.created_at) }}</div>
            </div>
            <div v-if="!docs.length && !loading" class="gsp-empty">
              {{ t(`${NS}.sourceFilterEmpty`) }}
            </div>
          </div>
        </div>
      </t-loading>

      <t-pagination
        v-if="shownTotal > PAGE_SIZE"
        class="gsp-pagination"
        size="small"
        :current="page"
        :total="shownTotal"
        :page-size="PAGE_SIZE"
        :show-page-size="false"
        :show-jumper="false"
        @change="onPageChange"
      />
    </div>

    <div class="gsp-footer">
      <div class="gsp-footer__left">
        <span class="gsp-footer__count">{{ t(`${NS}.sourceFilterSelected`, { count: draft.size }) }}</span>
        <button v-if="draft.size" type="button" class="gsp-footer__clear" @click="onClearDraft">
          {{ t(`${NS}.sourceFilterClear`) }}
        </button>
      </div>
      <div class="gsp-footer__right">
        <t-button variant="outline" size="small" @click="close">{{ t('common.cancel') }}</t-button>
        <t-button theme="primary" size="small" :disabled="!canConfirm || bulkLoading" @click="onConfirm">
          {{ t('common.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<style>
/* The dialog renders outside the component; same shell as BatchTagDialog. */
.gallery-source-dialog {
  overflow: hidden;
  padding: 0;
  border-radius: var(--app-radius-xs);
}

.gallery-source-dialog .t-dialog__header {
  min-height: auto;
  padding: 20px 20px 0;
}

.gallery-source-dialog .t-dialog__body {
  padding: 16px 20px 20px;
}

.gallery-source-dialog .t-dialog__close {
  top: 16px;
  right: 16px;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-secondary);
  transition: background var(--app-motion-fast) ease;
}

.gallery-source-dialog .t-dialog__close:hover {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

@media (max-width: 680px) {
  .gallery-source-dialog {
    width: calc(100vw - 24px) !important;
  }
}
</style>

<style scoped>
.gsp-heading {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding-right: 28px;
}

.gsp-heading__row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.gsp-heading__icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.gsp-heading__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 22px;
}

.gsp-heading__subtitle {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-weight: 400;
  line-height: 18px;
}

.gsp-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
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
  padding: 8px 10px;
  border-radius: var(--app-radius-md);
  background: var(--td-warning-color-1);
  color: var(--td-warning-color-7);
  font-size: var(--app-text-sm);
  line-height: 18px;
}

.gsp-overflow-hint .t-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.gsp-table {
  display: flex;
  flex-direction: column;
  min-height: 240px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  overflow: hidden;
}

.gsp-row {
  display: grid;
  grid-template-columns: 32px minmax(0, 1fr) 136px;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
}

.gsp-head {
  height: 36px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.gsp-head-cell {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0;
  border: none;
  background: none;
  font: inherit;
  color: inherit;
  cursor: pointer;
  text-align: left;
  justify-self: start;
}

.gsp-head-cell:hover {
  color: var(--td-text-color-primary);
}

.gsp-head-cell.is-active {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.gsp-head-cell:focus-visible {
  outline: 2px solid var(--app-focus-border);
  outline-offset: 2px;
}

.gsp-list {
  max-height: 324px;
  overflow-y: auto;
}

.gsp-item {
  height: 36px;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;
}

.gsp-item + .gsp-item {
  border-top: 1px solid var(--td-component-stroke);
}

.gsp-item:hover {
  background: var(--td-bg-color-container-hover);
}

.gsp-item.is-selected {
  background: var(--td-brand-color-light);
}

.gsp-col-check {
  display: flex;
  align-items: center;
}

.gsp-col-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
}

.gsp-col-time {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
}

.gsp-empty {
  padding: 48px 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.gsp-pagination {
  justify-content: flex-end;
}

.gsp-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
}

.gsp-footer__left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.gsp-footer__count {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.gsp-footer__clear {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  cursor: pointer;
}

.gsp-footer__clear:hover {
  color: var(--td-brand-color);
}

.gsp-footer__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
