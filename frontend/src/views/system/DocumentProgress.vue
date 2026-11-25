<template>
  <section class="document-progress" :aria-label="t('documentProgress.title')">
    <div class="dp-section-head">
      <div class="dp-section-copy">
        <h3 class="dp-section-title">{{ t('documentProgress.title') }}</h3>
        <p>{{ t('documentProgress.description') }}</p>
      </div>
      <span v-if="loadedOnce" class="dp-total-pill">
        {{ t('documentProgress.totalCount', { count: total }) }}
      </span>
    </div>

    <div class="dp-toolbar">
      <div class="dp-filters">
        <t-input
          v-model="searchDraft"
          class="dp-search-input"
          :maxlength="200"
          clearable
          :placeholder="t('documentProgress.search')"
          @enter="applySearch"
          @clear="clearSearch"
        >
          <template #prefix-icon>
            <t-icon name="search" />
          </template>
        </t-input>
        <t-button variant="outline" @click="applySearch">
          {{ t('documentProgress.searchButton') }}
        </t-button>
        <t-select
          v-model="status"
          class="dp-status-select"
          :options="statusOptions"
          :aria-label="t('documentProgress.filter')"
        />
      </div>

      <div class="dp-batch-group">
        <label class="dp-batch-limit">
          <span class="dp-batch-limit__label">{{ t('documentProgress.batchLimit') }}</span>
          <t-input-number
            v-model="count"
            class="dp-count-input"
            :min="1"
            :max="100"
            :decimal-places="0"
            :aria-label="t('documentProgress.count')"
          />
        </label>
        <t-tooltip :content="batchDisabledReason" :disabled="!batchDisabledReason">
          <span class="dp-batch-trigger">
            <t-popconfirm
              :content="batchConfirmContent"
              :disabled="batchDisabled"
              @confirm="startWiki()"
            >
              <t-button
                theme="primary"
                :variant="batchDisabled ? 'outline' : 'base'"
                :disabled="batchDisabled"
                :loading="starting && !startingID"
              >
                <template #icon><t-icon name="play-circle" /></template>
                {{ t('documentProgress.batchStart') }}
              </t-button>
            </t-popconfirm>
          </span>
        </t-tooltip>
      </div>
    </div>

    <div v-if="error" class="dp-error" role="alert">
      <div class="dp-error__copy">
        <t-icon name="error-circle" />
        <span>{{ error }}</span>
      </div>
      <t-button size="small" variant="outline" @click="reload(true)">
        {{ t('documentProgress.refresh') }}
      </t-button>
    </div>

    <div v-if="loading && !loadedOnce" class="dp-loading" aria-live="polite">
      <t-skeleton
        animation="gradient"
        :row-col="[
          { width: '100%', height: '40px' },
          { width: '100%', height: '52px' },
          { width: '100%', height: '52px' },
          { width: '100%', height: '52px' },
        ]"
      />
    </div>

    <div v-else-if="loadedOnce && documents.length === 0 && !error" class="dp-empty">
      <t-icon name="file-search" size="28px" />
      <strong class="dp-empty__title">{{ t('documentProgress.emptyTitle') }}</strong>
      <span class="dp-empty__desc">
        {{ status === 'unfinished' && !search ? t('documentProgress.emptyUnfinished') : t('documentProgress.description') }}
      </span>
      <div class="dp-empty__actions">
        <t-button v-if="search" size="small" variant="outline" @click="clearSearch">
          {{ t('documentProgress.clearSearch') }}
        </t-button>
        <t-button v-if="status !== 'all'" size="small" variant="outline" @click="status = 'all'">
          {{ t('documentProgress.viewAll') }}
        </t-button>
      </div>
    </div>

    <div v-else class="data-table-shell dp-table-shell">
      <t-table
        row-key="id"
        :data="documents"
        :columns="columns"
        :loading="loading"
        size="medium"
        :max-height="560"
        hover
      >
        <template #title="{ row }">
          <div class="dp-doc-cell">
            <span class="dp-doc-title" :title="row.title">{{ row.title }}</span>
            <span class="dp-doc-meta" :title="row.knowledge_base_name">
              {{ row.knowledge_base_name }} · {{ t('documentProgress.attempt', { value: row.attempt }) }}
            </span>
          </div>
        </template>

        <template #parse_status="{ row }">
          <span class="dp-status" :class="`dp-status--${stateTone(row.ingestion_status)}`">
            <i />{{ stateLabel(row.ingestion_status) }}
          </span>
        </template>

        <template #wiki_status="{ row }">
          <div class="dp-wiki-cell">
            <span class="dp-status" :class="`dp-status--${stateTone(row.wiki_status)}`">
              <i />{{ stateLabel(row.wiki_status) }}
            </span>
            <span v-if="row.wiki_pending" class="dp-wiki-meta">
              {{ t('documentProgress.pendingOps', { count: row.wiki_pending, retries: row.wiki_retries }) }}
            </span>
          </div>
        </template>

        <template #failure_count="{ row }">
          <t-button
            v-if="row.failure_count > 0"
            variant="text"
            size="small"
            theme="danger"
            class="dp-history-btn"
            @click="openDetail(row.id)"
          >
            {{ t('documentProgress.historyCount', { count: row.failure_count }) }}
          </t-button>
          <span v-else class="dp-muted">—</span>
        </template>

        <template #actions="{ row }">
          <div class="dp-row-actions">
            <t-button variant="text" size="small" @click="openDetail(row.id)">
              {{ t('documentProgress.details') }}
            </t-button>
            <t-popconfirm
              v-if="row.can_start"
              :content="t('documentProgress.confirmOne', { title: row.title })"
              @confirm="startWiki(row.id)"
            >
              <t-button
                variant="text"
                size="small"
                theme="primary"
                :disabled="starting"
                :loading="startingID === row.id"
              >
                {{ t('documentProgress.startNow') }}
              </t-button>
            </t-popconfirm>
          </div>
        </template>
      </t-table>
    </div>

    <div v-if="total > pageSize" class="dp-pagination">
      <t-pagination
        v-model:current="page"
        :total="total"
        :page-size="pageSize"
        :show-page-size="false"
        size="small"
      />
    </div>

    <SettingDrawer
      v-model:visible="detailVisible"
      :title="detail?.document.title || t('documentProgress.details')"
      :description="detail?.document.knowledge_base_name || ''"
      icon="file-paste"
      width="640px"
      :min-width="480"
      :max-width="960"
      storage-key="setting-drawer:width:document-progress"
      hide-footer
    >
      <div v-if="detailError" role="alert" class="dp-error">
        <div class="dp-error__copy">
          <t-icon name="error-circle" />
          <span>{{ detailError }}</span>
        </div>
      </div>
      <div v-else-if="detailLoading" class="dp-drawer-loading">
        <t-loading size="small" />
      </div>
      <template v-else-if="detail">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('documentProgress.currentStatus') }}</h4>
          <div class="dp-detail-summary">
            <div class="dp-detail-kv">
              <span class="dp-detail-kv__label">{{ t('documentProgress.ingestion') }}</span>
              <span class="dp-status" :class="`dp-status--${stateTone(detail.document.ingestion_status)}`">
                <i />{{ stateLabel(detail.document.ingestion_status) }}
              </span>
            </div>
            <div class="dp-detail-kv">
              <span class="dp-detail-kv__label">{{ t('documentProgress.wiki') }}</span>
              <span class="dp-status" :class="`dp-status--${stateTone(detail.document.wiki_status)}`">
                <i />{{ stateLabel(detail.document.wiki_status) }}
              </span>
            </div>
            <div class="dp-detail-kv">
              <span class="dp-detail-kv__label">{{ t('documentProgress.attempt', { value: detail.document.attempt }) }}</span>
              <span v-if="detail.document.wiki_pending" class="dp-detail-kv__meta">
                {{ t('documentProgress.pendingOps', { count: detail.document.wiki_pending, retries: detail.document.wiki_retries }) }}
              </span>
              <span v-else class="dp-muted">—</span>
            </div>
          </div>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('documentProgress.stagesTitle') }}</h4>
          <p v-if="!detail.document.stages.length" class="dp-drawer-note">{{ t('documentProgress.noStages') }}</p>
          <ul v-else class="dp-stages-panel">
            <li v-for="stage in detail.document.stages" :key="stage.name" class="dp-stage-row">
              <div class="dp-stage-main">
                <span class="dp-stage-name">{{ stageLabel(stage.name) }}</span>
                <span v-if="stage.error_code" class="dp-stage-code">{{ stage.error_code }}</span>
              </div>
              <div class="dp-stage-right">
                <span class="dp-status" :class="`dp-status--${stateTone(stage.status)}`">
                  <i />{{ stateLabel(stage.status) }}
                </span>
                <span v-if="stage.finished_at" class="dp-stage-time">{{ formatTime(stage.finished_at) }}</span>
              </div>
            </li>
          </ul>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('documentProgress.history') }}</h4>
          <p class="dp-drawer-note">{{ t('documentProgress.historyDescription') }}</p>
          <p v-if="!detail.failures.length" class="dp-drawer-empty">{{ t('documentProgress.noHistory') }}</p>
          <div v-else class="dp-failure-list">
            <article v-for="failure in detail.failures" :key="failure.id" class="dp-failure-card">
              <div class="dp-failure-head">
                <strong class="dp-failure-type">{{ failure.task_type }}</strong>
                <span class="dp-failure-sep" aria-hidden="true">·</span>
                <span class="dp-failure-meta">{{ t('documentProgress.failureAttempts', { count: failure.fail_count }) }}</span>
                <span class="dp-failure-time">{{ formatTime(failure.failed_at) }}</span>
              </div>
              <pre class="dp-failure-error">{{ failure.last_error }}</pre>
            </article>
          </div>
          <p v-if="detail.has_more_failures" class="dp-drawer-note dp-drawer-note--tail">
            {{ t('documentProgress.historyLimit') }}
          </p>
        </section>
      </template>
    </SettingDrawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  getDocumentProgress, listDocumentProgress, startDocumentWiki,
  type DocumentProgress, type DocumentProgressDetail, type DocumentProgressStatus, type WikiStartResult,
} from '@/api/system/documentProgress'

const props = withDefaults(defineProps<{
  refreshToken?: string
  refreshSeq?: number
  silentRefreshSeq?: number
}>(), {
  refreshToken: '',
  refreshSeq: 0,
  silentRefreshSeq: 0,
})

const emit = defineEmits<{
  (e: 'loading-change', loading: boolean): void
}>()

const { t, locale } = useI18n()
const documents = ref<DocumentProgress[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 25
const searchDraft = ref('')
const search = ref('')
const status = ref<DocumentProgressStatus>('unfinished')
const count = ref<number | undefined>(1)
const validCount = computed(() => Number.isInteger(count.value) && Number(count.value) >= 1 && Number(count.value) <= 100)
const safeCount = computed(() => validCount.value ? Number(count.value) : 1)
const batchStatusAllowed = computed(() => ['all', 'unfinished'].includes(status.value))
const loading = ref(false)
const loadedOnce = ref(false)
const starting = ref(false)
const startingID = ref('')
const error = ref('')
const detail = ref<DocumentProgressDetail>()
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
let listVersion = 0
let detailVersion = 0
let alive = true

const batchDisabled = computed(() =>
  starting.value || !batchStatusAllowed.value || !validCount.value || (loadedOnce.value && total.value === 0),
)
const batchDisabledReason = computed(() => {
  if (starting.value || !validCount.value) return ''
  if (!batchStatusAllowed.value) return t('documentProgress.batchDisabledStatus')
  if (loadedOnce.value && total.value === 0) return t('documentProgress.batchDisabledEmpty')
  return ''
})
const batchConfirmContent = computed(() => {
  const effectiveSearch = search.value.trim()
  if (effectiveSearch) {
    return t('documentProgress.confirmBatchScoped', { count: safeCount.value, search: effectiveSearch })
  }
  return t('documentProgress.confirmBatchAll', { count: safeCount.value })
})

const statusOptions = computed(() => [
  { value: 'unfinished', label: t('documentProgress.unfinished') },
  { value: 'all', label: t('documentProgress.all') },
  { value: 'failed', label: t('documentProgress.states.failed') },
  { value: 'completed', label: t('documentProgress.states.completed') },
])
const columns = computed(() => [
  { colKey: 'title', title: t('documentProgress.document'), minWidth: 260 },
  { colKey: 'parse_status', title: t('documentProgress.ingestion'), width: 112 },
  { colKey: 'wiki_status', title: t('documentProgress.wiki'), width: 176 },
  { colKey: 'failure_count', title: t('documentProgress.history'), width: 112, align: 'center' as const },
  { colKey: 'actions', title: t('documentProgress.actions'), width: 148, align: 'right' as const },
])

function stateLabel(state: string) {
  const labels: Record<string, string> = {
    pending: t('documentProgress.states.pending'), processing: t('documentProgress.states.processing'),
    running: t('documentProgress.states.processing'), finalizing: t('documentProgress.states.finalizing'),
    completed: t('documentProgress.states.completed'), done: t('documentProgress.states.completed'),
    failed: t('documentProgress.states.failed'), skipped: t('documentProgress.states.skipped'),
    cancelled: t('documentProgress.states.cancelled'), deleting: t('documentProgress.states.deleting'),
    queued: t('documentProgress.states.queued'), retrying: t('documentProgress.states.retrying'),
    disabled: t('documentProgress.states.disabled'), not_started: t('documentProgress.states.notStarted'),
    ready: t('documentProgress.states.ready'),
  }
  return labels[state] || state
}

function stateTone(state: string): string {
  switch (state) {
    case 'completed':
    case 'done':
    case 'ready':
      return 'success'
    case 'processing':
    case 'running':
    case 'finalizing':
      return 'working'
    case 'queued':
    case 'pending':
      return 'waiting'
    case 'retrying':
      return 'attention'
    case 'failed':
      return 'danger'
    default:
      return 'idle'
  }
}

function stageLabel(name: string) {
  const labels: Record<string, string> = {
    docreader: t('documentProgress.stages.docreader'), chunking: t('documentProgress.stages.chunking'),
    embedding: t('documentProgress.stages.embedding'), multimodal: t('documentProgress.stages.multimodal'),
    postprocess: t('documentProgress.stages.postprocess'), 'postprocess.wiki': t('documentProgress.wiki'),
  }
  return labels[name] || name
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(locale?.value || undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

async function reload(showSpinner = true) {
  const version = ++listVersion
  if (showSpinner) {
    loading.value = true
    emit('loading-change', true)
  }
  try {
    const response = await listDocumentProgress({ page: page.value, page_size: pageSize, status: status.value, search: search.value })
    if (!alive || version !== listVersion) return
    const lastPage = Math.max(1, Math.ceil(response.data.total / pageSize))
    if (page.value > lastPage) {
      total.value = response.data.total
      page.value = lastPage
      return
    }
    documents.value = response.data.documents
    total.value = response.data.total
    loadedOnce.value = true
    error.value = ''
  } catch {
    if (alive && version === listVersion) error.value = t('documentProgress.loadError')
  } finally {
    if (alive && version === listVersion && showSpinner) {
      loading.value = false
      emit('loading-change', false)
    }
  }
}

function applySearch() {
  search.value = searchDraft.value.trim()
  if (page.value === 1) void reload(true)
  else page.value = 1
}

function clearSearch() {
  searchDraft.value = ''
  applySearch()
}

async function openDetail(id: string) {
  const version = ++detailVersion
  detailVisible.value = true
  detailLoading.value = true
  detailError.value = ''
  detail.value = undefined
  try {
    const response = await getDocumentProgress(id)
    if (alive && version === detailVersion) detail.value = response.data
  } catch {
    if (alive && version === detailVersion) detailError.value = t('documentProgress.loadError')
  } finally {
    if (alive && version === detailVersion) detailLoading.value = false
  }
}

async function startWiki(id?: string) {
  if (starting.value || (!id && (!validCount.value || !batchStatusAllowed.value))) return
  starting.value = true
  startingID.value = id || ''
  try {
    const response = await startDocumentWiki({ count: id ? 1 : Number(count.value), ...(id ? { knowledge_id: id } : { search: search.value }) })
    if (!alive) return
    MessagePlugin.success(t('documentProgress.started', { count: response.data.started, queued: response.data.already_queued }))
  } catch (cause) {
    if (!alive) return
    const partial = (cause as { data?: Partial<WikiStartResult> })?.data
    MessagePlugin.error(t('documentProgress.startError', { count: Number(partial?.started) || 0 }))
  } finally {
    if (alive) {
      starting.value = false
      startingID.value = ''
      await reload(false)
    }
  }
}

watch(page, () => { void reload(true) })
watch(status, () => { if (page.value === 1) void reload(true); else page.value = 1 })
watch(() => props.refreshToken, () => { void reload(false) })
watch(() => props.refreshSeq, (seq) => { if (seq > 0) void reload(true) })
watch(() => props.silentRefreshSeq, (seq) => { if (seq > 0) void reload(false) })
watch(detailVisible, visible => { if (!visible) { detailVersion++; detailLoading.value = false } })
onMounted(() => { void reload(true) })
onUnmounted(() => { alive = false; listVersion++; detailVersion++ })
</script>

<style scoped lang="less">
.document-progress {
  color: var(--td-text-color-primary);
}

.dp-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 16px;
}

.dp-section-copy {
  min-width: 0;

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    line-height: 1.55;
  }
}

.dp-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 6px;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 1.35;

  &::before {
    content: '';
    flex-shrink: 0;
    width: 3px;
    height: 15px;
    border-radius: 2px;
    background: var(--td-brand-color);
  }
}

.dp-total-pill {
  flex-shrink: 0;
  margin-top: 2px;
  padding: 4px 10px;
  border-radius: var(--app-radius-pill);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.4;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  background: var(--td-bg-color-secondarycontainer);
}

.dp-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}

.dp-filters {
  display: flex;
  align-items: center;
  flex: 1 1 360px;
  min-width: 0;
  gap: 8px;
}

.dp-search-input {
  flex: 1 1 220px;
  min-width: 160px;
  max-width: 360px;
}

.dp-status-select {
  width: 132px;
  flex: 0 0 132px;
}

.dp-batch-group {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.dp-batch-limit {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  white-space: nowrap;
}

.dp-batch-limit__label {
  user-select: none;
}

.dp-count-input {
  width: 112px;
}

.dp-batch-trigger {
  display: inline-flex;
}

.dp-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  color: var(--td-error-color);
  font-size: var(--app-text-md);
  background: var(--td-error-color-1);
}

.dp-error__copy {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.dp-loading {
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.dp-empty {
  display: flex;
  min-height: 210px;
  padding: 28px 20px;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 8px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.dp-empty__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  font-weight: 500;
}

.dp-empty__desc {
  max-width: 420px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.55;
}

.dp-empty__actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
}

.data-table-shell.dp-table-shell {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    height: 40px;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    font-weight: 500;
    letter-spacing: 0.01em;
    white-space: nowrap;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td) {
    height: 56px;
    padding-top: 10px;
    padding-bottom: 10px;
    font-size: var(--app-text-base);
    font-variant-numeric: tabular-nums;
  }

  &:deep(.t-table th.t-align-center),
  &:deep(.t-table td.t-align-center) {
    text-align: center;
  }

  &:deep(.t-table th.t-align-right),
  &:deep(.t-table td.t-align-right) {
    text-align: right;
  }

  &:deep(.t-table__body tr:last-child td) {
    border-bottom: 0;
  }
}

.dp-doc-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.dp-doc-title {
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dp-doc-meta {
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dp-wiki-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.dp-wiki-meta {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  line-height: 1.4;
  font-variant-numeric: tabular-nums;
}

.dp-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  white-space: nowrap;

  i {
    width: 6px;
    height: 6px;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--td-text-color-placeholder);
  }

  &--success {
    color: var(--td-success-color);
  }

  &--success i {
    background: var(--td-success-color);
  }

  &--working {
    color: var(--td-brand-color);
  }

  &--working i {
    background: var(--td-brand-color);
  }

  &--waiting i {
    background: var(--td-text-color-secondary);
  }

  &--attention {
    color: var(--td-warning-color);
  }

  &--attention i {
    background: var(--td-warning-color);
  }

  &--danger {
    color: var(--td-error-color);
  }

  &--danger i {
    background: var(--td-error-color);
  }
}

.dp-history-btn {
  padding: 0 4px;
  font-variant-numeric: tabular-nums;
}

.dp-muted {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.dp-row-actions {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
}

.dp-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: 14px;
}

.dp-drawer-loading {
  display: flex;
  min-height: 160px;
  align-items: center;
  justify-content: center;
}

.dp-detail-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-secondarycontainer);
}

.dp-detail-kv {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}

.dp-detail-kv__label {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.dp-detail-kv__meta {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
}

.dp-stages-panel {
  margin: 0;
  padding: 0;
  list-style: none;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.dp-stage-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 14px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: 0;
  }
}

.dp-stage-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.dp-stage-name {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
}

.dp-stage-code {
  padding: 1px 6px;
  border-radius: var(--app-radius-sm);
  color: var(--td-error-color);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: var(--app-text-xs);
  background: var(--td-error-color-1);
}

.dp-stage-right {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.dp-stage-time {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-variant-numeric: tabular-nums;
}

.dp-drawer-note {
  margin: 0 0 12px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.55;

  &--tail {
    margin: 12px 0 0;
    color: var(--td-text-color-placeholder);
  }
}

.dp-drawer-empty {
  margin: 0;
  padding: 18px 16px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  text-align: center;
}

.dp-failure-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dp-failure-card {
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.dp-failure-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.dp-failure-type {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.dp-failure-sep {
  color: var(--td-text-color-placeholder);
}

.dp-failure-meta {
  font-variant-numeric: tabular-nums;
}

.dp-failure-time {
  margin-left: auto;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-variant-numeric: tabular-nums;
}

.dp-failure-error {
  margin: 0;
  padding: 8px 10px;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: var(--app-text-xs);
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--td-bg-color-secondarycontainer);
}

@media (max-width: 860px) {
  .dp-section-head {
    flex-direction: column;
    gap: 8px;
  }

  .dp-toolbar {
    align-items: stretch;
  }

  .dp-filters {
    flex: 1 1 100%;
    flex-wrap: wrap;
  }

  .dp-search-input {
    max-width: none;
  }

  .dp-batch-group {
    width: 100%;
    justify-content: space-between;
  }

  .dp-detail-summary {
    grid-template-columns: 1fr;
  }
}
</style>
