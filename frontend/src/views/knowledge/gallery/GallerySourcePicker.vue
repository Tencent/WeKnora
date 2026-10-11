<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeFiles } from '@/api/knowledge-base'

// GallerySourcePicker is the "filter by source document" dialog of the image
// gallery: a paginated, keyword-searchable, sortable checkbox list of the
// knowledge base's documents. Confirming hands the selected ids to the
// gallery; the selection starts from what is already active so reopening
// shows the state the user last committed.
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

const keyword = ref('')
const page = ref(1)
const total = ref(0)
const loading = ref(false)
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
// Names of every id in draft, so rows can label selected documents that
// scrolled onto an earlier page.
const namesById = ref<Record<string, string>>({})

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
    for (const d of docs.value) namesById.value[d.id] = d.file_name
  } catch (_) {
    if (token === fetchToken) {
      docs.value = []
      total.value = 0
    }
  } finally {
    if (token === fetchToken) loading.value = false
  }
}

watch(
  () => props.visible,
  (open) => {
    if (!open) return
    draft.value = new Set(props.selected)
    keyword.value = ''
    page.value = 1
    sortBy.value = 'created_at'
    sortDesc.value = true
    void loadDocs()
  },
)

watch(keyword, () => {
  page.value = 1
  void loadDocs()
})

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

// The ids of every document the current search hits, fetched in one page
// (the guards above keep that within the backend's page_size ceiling).
async function fetchHitIds(): Promise<string[]> {
  const res: any = await listKnowledgeFiles(props.kbId, {
    page: 1,
    page_size: SAFE_HITS,
    keyword: keyword.value.trim() || undefined,
    sort_by: sortBy.value,
    sort_order: sortDesc.value ? 'desc' : 'asc',
  })
  const items: any[] = res?.data ?? []
  return items.map((it) => String(it.id))
}

// Header checkbox: when the current page is fully selected the click means
// "drop these hits from the selection", otherwise "add every hit" — both
// across all pages, merged into the draft so earlier keywords survive.
async function onToggleAll() {
  if (!canSelectAll.value) return
  loading.value = true
  try {
    const ids = await fetchHitIds()
    if (!ids.length) return
    if (pageAllSelected.value) {
      const next = new Set(draft.value)
      for (const id of ids) next.delete(id)
      draft.value = next
    } else {
      const next = new Set(draft.value)
      for (const id of ids) next.add(id)
      draft.value = next
    }
  } catch (_) {
    // keep the draft untouched on failure
  } finally {
    loading.value = false
  }
}

function onConfirm() {
  if (draft.value.size > SAFE_HITS) {
    MessagePlugin.warning(t(`${NS}.sourceFilterTooMany`))
    return
  }
  emit('confirm', Array.from(draft.value))
  emit('update:visible', false)
}

function onClear() {
  draft.value = new Set()
  emit('confirm', [])
  emit('update:visible', false)
}

const draftCount = () => draft.value.size

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
    :header="t(`${NS}.sourceFilterTitle`)"
    width="760px"
    :confirm-btn="{
      content: draftCount() ? t(`${NS}.sourceFilterConfirmWithCount`, { count: draftCount() }) : t(`${NS}.sourceFilterConfirm`),
      disabled: !draftCount(),
    }"
    :cancel-btn="t(`${NS}.sourceFilterCancel`)"
    @confirm="onConfirm"
    @close="emit('update:visible', false)"
  >
    <div class="gsp-body">
      <div class="gsp-actions">
        <t-input
          v-model="keyword"
          clearable
          :placeholder="t(`${NS}.sourceFilterSearch`)"
          class="gsp-search"
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
        <t-button v-if="props.selected.length" variant="text" theme="default" @click="onClear">
          {{ t(`${NS}.sourceFilterClear`) }}
        </t-button>
      </div>

      <div v-if="overflown" class="gsp-overflow-hint">
        {{
          keyword.trim()
            ? t(`${NS}.sourceFilterOverflowHits`, { total: total, cap: shownTotal })
            : t(`${NS}.sourceFilterOverflowRecent`, { cap: shownTotal })
        }}
      </div>

      <t-loading :loading="loading" size="small" show-overlay>
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
              <t-icon
                v-if="sortBy === 'file_name'"
                :name="sortDesc ? 'arrow-down' : 'arrow-up'"
                size="14px"
              />
            </button>
            <button
              type="button"
              class="gsp-head-cell"
              :class="{ 'is-active': sortBy === 'created_at' }"
              @click="toggleSort('created_at')"
            >
              <span>{{ t(`${NS}.sourceFilterColTime`) }}</span>
              <t-icon
                v-if="sortBy === 'created_at'"
                :name="sortDesc ? 'arrow-down' : 'arrow-up'"
                size="14px"
              />
            </button>
          </div>

          <div class="gsp-list">
            <div v-for="d in docs" :key="d.id" class="gsp-row">
              <div class="gsp-col-check">
                <t-checkbox
                  :checked="draft.has(d.id)"
                  @change="(checked: boolean) => toggle(d.id, checked)"
                />
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
        size="small"
        :current="page"
        :total="shownTotal"
        :page-size="PAGE_SIZE"
        :show-page-size="false"
        @change="onPageChange"
      />
    </div>
  </t-dialog>
</template>

<style scoped>
.gsp-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.gsp-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.gsp-search {
  flex: 1;
}

.gsp-overflow-hint {
  padding: 6px 10px;
  border-radius: 6px;
  background: var(--td-warning-color-1, rgba(236, 162, 0, 0.1));
  color: var(--td-warning-color-7, #a2790a);
  font-size: 12px;
  line-height: 1.5;
}

.gsp-table {
  display: flex;
  flex-direction: column;
  min-height: 200px;
}

.gsp-row {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr) 150px;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
}

.gsp-head {
  height: 36px;
  border-bottom: 1px solid var(--td-component-stroke, var(--td-border-level-1-color, #e7e7e7));
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.gsp-head-cell {
  display: flex;
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

.gsp-head-cell.is-active {
  color: var(--td-brand-color);
  font-weight: 600;
}

.gsp-list {
  max-height: 320px;
  overflow-y: auto;
  padding-top: 4px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.gsp-col-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}

.gsp-col-time {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

.gsp-empty {
  padding: 32px 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
