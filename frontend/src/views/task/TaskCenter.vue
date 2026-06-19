<template>
  <main class="task-center">
    <header class="task-center__header">
      <div>
        <h1>{{ $t('taskCenter.title') }}</h1>
        <p>{{ $t('taskCenter.subtitle') }}</p>
      </div>
      <div class="task-center__toolbar">
        <t-input
          v-model="searchText"
          class="task-center__search"
          clearable
          :placeholder="$t('taskCenter.searchPlaceholder')"
          @enter="applySearch"
          @clear="applySearch"
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select v-model="kindFilter" class="task-center__kind" clearable :placeholder="$t('taskCenter.kind.all')" @change="reloadFromFirstPage">
          <t-option v-for="item in kindOptions" :key="item.value" :value="item.value" :label="item.label" />
        </t-select>
        <t-tooltip :content="$t('common.refresh')" placement="bottom">
          <t-button shape="square" variant="outline" :loading="loading" @click="refreshAll">
            <template #icon><t-icon name="refresh" /></template>
          </t-button>
        </t-tooltip>
      </div>
    </header>

    <section class="task-center__summary" aria-live="polite">
      <button
        v-for="card in summaryCards"
        :key="card.key"
        class="summary-card"
        :class="[`summary-card--${card.tone}`, { 'summary-card--active': activeTab === card.tab }]"
        type="button"
        @click="selectTab(card.tab)"
      >
        <span class="summary-card__label">{{ card.label }}</span>
        <strong>{{ card.value }}</strong>
      </button>
    </section>

    <section class="task-center__content">
      <div class="task-center__tabs">
        <t-tabs v-model="activeTab" @change="reloadFromFirstPage">
          <t-tab-panel v-for="tab in tabs" :key="tab.value" :value="tab.value" :label="tab.label" />
        </t-tabs>
        <div class="task-center__bulk">
          <t-popconfirm
            :content="$t('taskCenter.retryAllConfirm')"
            :confirm-btn="{ content: $t('taskCenter.actions.retryAll'), theme: 'primary' }"
            :cancel-btn="{ content: $t('common.cancel') }"
            placement="bottom-right"
            @confirm="retryAll"
          >
            <t-button size="small" variant="outline" :loading="bulkLoading" :disabled="!canManageBatch">
              <template #icon><t-icon name="refresh" /></template>
              {{ $t('taskCenter.actions.retryAll') }}
            </t-button>
          </t-popconfirm>
          <t-popconfirm
            :content="$t('taskCenter.deleteRecordsConfirm')"
            :confirm-btn="{ content: $t('taskCenter.actions.deleteRecords'), theme: 'danger' }"
            :cancel-btn="{ content: $t('common.cancel') }"
            placement="bottom-right"
            @confirm="deleteRecords"
          >
            <t-button size="small" theme="danger" variant="outline" :loading="bulkLoading" :disabled="!canManageBatch">
              <template #icon><t-icon name="delete" /></template>
              {{ $t('taskCenter.actions.deleteRecords') }}
            </t-button>
          </t-popconfirm>
        </div>
      </div>

      <div class="task-table">
        <t-table row-key="job_id" :data="jobs" :columns="columns" :loading="loading" size="medium" hover stripe>
          <template #display_name="{ row }">
            <button class="task-link" type="button" @click="openDetail(row)">
              <span>{{ taskTitle(row) }}</span>
              <small>{{ row.job_id }}</small>
            </button>
          </template>
          <template #state="{ row }">
            <t-tag :theme="stateTheme(row.state)" size="small" variant="light-outline">
              {{ stateLabel(row.state) }}
            </t-tag>
          </template>
          <template #kind="{ row }">{{ kindLabel(row.kind) }}</template>
          <template #updated_at="{ row }">{{ formatDate(row.updated_at) }}</template>
          <template #last_error="{ row }">
            <span v-if="row.last_error" class="task-error" :title="row.last_error">{{ row.last_error }}</span>
            <span v-else class="muted">-</span>
          </template>
          <template #actions="{ row }">
            <div class="row-actions">
              <t-tooltip :content="$t('taskCenter.actions.detail')" placement="top">
                <t-button shape="square" variant="text" size="small" @click="openDetail(row)">
                  <template #icon><t-icon name="browse" /></template>
                </t-button>
              </t-tooltip>
              <t-tooltip :content="$t('taskCenter.actions.retry')" placement="top">
                <t-button shape="square" variant="text" size="small" :disabled="!row.capabilities?.can_retry" @click="retryOne(row)">
                  <template #icon><t-icon name="refresh" /></template>
                </t-button>
              </t-tooltip>
              <t-popconfirm
                :content="$t('taskCenter.cancelConfirm')"
                :confirm-btn="{ content: $t('taskCenter.actions.cancel'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }"
                placement="left"
                @confirm="cancelOne(row)"
              >
                <t-tooltip :content="$t('taskCenter.actions.cancel')" placement="top">
                  <t-button shape="square" theme="danger" variant="text" size="small" :disabled="!row.capabilities?.can_cancel" @click.stop>
                    <template #icon><t-icon name="stop-circle" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </div>
          </template>
        </t-table>
        <div v-if="!loading && jobs.length === 0" class="task-empty">
          <t-empty :description="$t('taskCenter.empty')" />
        </div>
        <footer class="task-table__footer">
          <t-pagination
            v-model="page"
            v-model:page-size="pageSize"
            :total="total"
            size="small"
            show-jumper
            show-page-number
            show-page-size
            :page-size-options="pageSizeOptions"
            @change="loadJobs"
          />
        </footer>
      </div>
    </section>

    <t-drawer v-model:visible="detailVisible" :header="$t('taskCenter.detail.title')" size="760px" :footer="false" destroy-on-close>
      <div v-if="selectedJob" class="task-detail">
        <div class="task-detail__head">
          <div>
            <h2>{{ taskTitle(selectedJob) }}</h2>
            <p>{{ selectedJob.job_id }}</p>
          </div>
          <t-tag :theme="stateTheme(selectedJob.state)" variant="light-outline">{{ stateLabel(selectedJob.state) }}</t-tag>
        </div>

        <dl class="detail-grid">
          <div><dt>{{ $t('taskCenter.columns.kind') }}</dt><dd>{{ kindLabel(selectedJob.kind) }}</dd></div>
          <div><dt>{{ $t('taskCenter.detail.attempt') }}</dt><dd>{{ selectedJob.process_attempt }}</dd></div>
          <div><dt>{{ $t('taskCenter.detail.scope') }}</dt><dd>{{ selectedJob.scope_id }}</dd></div>
          <div><dt>{{ $t('taskCenter.columns.updatedAt') }}</dt><dd>{{ formatDate(selectedJob.updated_at) }}</dd></div>
        </dl>

        <section class="detail-section" v-if="selectedJob.last_error">
          <h3>{{ $t('taskCenter.detail.error') }}</h3>
          <pre>{{ selectedJob.last_error }}</pre>
        </section>

        <section class="detail-section">
          <h3>{{ $t('taskCenter.detail.metadata') }}</h3>
          <pre>{{ formatJson(selectedJob.metadata) }}</pre>
        </section>

        <section class="detail-section">
          <h3>{{ $t('taskCenter.detail.executions') }}</h3>
          <t-table row-key="execution_id" :data="executions" :columns="executionColumns" size="small" hover :loading="detailLoading">
            <template #state="{ row }">
              <t-tag :theme="executionTheme(row.state)" size="small" variant="light-outline">
                {{ executionStateLabel(row.state) }}
              </t-tag>
            </template>
            <template #enqueued_at="{ row }">{{ formatDate(row.enqueued_at) }}</template>
            <template #last_error="{ row }">
              <span v-if="row.last_error" class="task-error" :title="row.last_error">{{ row.last_error }}</span>
              <span v-else class="muted">-</span>
            </template>
          </t-table>
        </section>
      </div>
    </t-drawer>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import {
  cancelTask,
  deleteTaskRecords,
  getTaskJob,
  getTaskSummary,
  listTaskExecutions,
  listTaskJobs,
  retryTask,
  retryTaskBatch,
  type TaskExecution,
  type TaskExecutionState,
  type TaskJob,
  type TaskJobState,
  type TaskSummary,
} from '@/api/task-center'

const { t } = useI18n()
const authStore = useAuthStore()

const activeTab = ref('all')
const searchText = ref('')
const kindFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const bulkLoading = ref(false)
const detailLoading = ref(false)
const detailVisible = ref(false)
const selectedJob = ref<TaskJob | null>(null)
const jobs = ref<TaskJob[]>([])
const executions = ref<TaskExecution[]>([])
const summary = ref<TaskSummary>({ queued: 0, processing: 0, succeeded: 0, failed: 0, canceled: 0 })
const pageSizeOptions = [10, 20, 50, 100]
let pollTimer: number | undefined

const canManageBatch = computed(() => authStore.hasRole('admin'))

const tabs = computed(() => [
  { value: 'all', label: t('taskCenter.tabs.all') },
  { value: 'queued', label: t('taskCenter.tabs.queued') },
  { value: 'processing', label: t('taskCenter.tabs.processing') },
  { value: 'succeeded', label: t('taskCenter.tabs.succeeded') },
  { value: 'failed_or_canceled', label: t('taskCenter.tabs.failedOrCanceled') },
])

const kindOptions = computed(() => [
  { value: 'upload', label: t('taskCenter.kind.upload') },
  { value: 'reparse', label: t('taskCenter.kind.reparse') },
  { value: 'datasource_sync', label: t('taskCenter.kind.datasourceSync') },
  { value: 'rebuild_wiki', label: t('taskCenter.kind.rebuildWiki') },
])

const summaryCards = computed(() => [
  { key: 'queued', tab: 'queued', tone: 'queued', label: t('taskCenter.summary.queued'), value: summary.value.queued },
  { key: 'processing', tab: 'processing', tone: 'processing', label: t('taskCenter.summary.processing'), value: summary.value.processing },
  { key: 'succeeded', tab: 'succeeded', tone: 'succeeded', label: t('taskCenter.summary.succeeded'), value: summary.value.succeeded },
  {
    key: 'failed',
    tab: 'failed_or_canceled',
    tone: 'failed',
    label: t('taskCenter.summary.failedOrCanceled'),
    value: summary.value.failed + summary.value.canceled,
  },
])

const columns = computed(() => [
  { colKey: 'display_name', title: t('taskCenter.columns.task'), minWidth: 220, ellipsis: true },
  { colKey: 'state', title: t('taskCenter.columns.state'), width: 132 },
  { colKey: 'kind', title: t('taskCenter.columns.kind'), width: 132 },
  { colKey: 'updated_at', title: t('taskCenter.columns.updatedAt'), width: 176 },
  { colKey: 'last_error', title: t('taskCenter.columns.error'), minWidth: 220, ellipsis: true },
  { colKey: 'actions', title: t('taskCenter.columns.actions'), width: 128, align: 'left' },
])

const executionColumns = computed(() => [
  { colKey: 'execution_id', title: t('taskCenter.detail.executionId'), minWidth: 180, ellipsis: true },
  { colKey: 'state', title: t('taskCenter.columns.state'), width: 108 },
  { colKey: 'retry_count', title: t('taskCenter.detail.retryCount'), width: 96 },
  { colKey: 'enqueued_at', title: t('taskCenter.detail.enqueuedAt'), width: 160 },
  { colKey: 'last_error', title: t('taskCenter.columns.error'), minWidth: 180, ellipsis: true },
])

function queryParams() {
  return {
    state: activeTab.value === 'all' ? '' : activeTab.value,
    kind: kindFilter.value,
    q: searchText.value.trim(),
    page: page.value,
    page_size: pageSize.value,
    sort: 'updated_at_desc',
  }
}

async function loadSummary() {
  summary.value = await getTaskSummary({
    kind: kindFilter.value,
    q: searchText.value.trim(),
  })
}

async function loadJobs() {
  loading.value = true
  try {
    const result = await listTaskJobs(queryParams())
    jobs.value = result.items || []
    total.value = result.total || 0
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function refreshAll() {
  await Promise.all([loadSummary(), loadJobs()])
}

function reloadFromFirstPage() {
  page.value = 1
  void refreshAll()
}

function applySearch() {
  reloadFromFirstPage()
}

function selectTab(tab: string) {
  activeTab.value = tab
  reloadFromFirstPage()
}

async function openDetail(row: TaskJob) {
  detailVisible.value = true
  detailLoading.value = true
  selectedJob.value = row
  try {
    const [job, execs] = await Promise.all([getTaskJob(row.job_id), listTaskExecutions(row.job_id)])
    selectedJob.value = job
    executions.value = execs || []
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.detailFailed'))
  } finally {
    detailLoading.value = false
  }
}

async function retryOne(row: TaskJob) {
  if (!row.capabilities?.can_retry) return
  try {
    await retryTask(row.job_id)
    MessagePlugin.success(t('taskCenter.messages.retryStarted'))
    await refreshAll()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.retryFailed'))
  }
}

async function retryAll() {
  bulkLoading.value = true
  try {
    const result = await retryTaskBatch({ ...queryParams(), page: undefined, page_size: undefined })
    MessagePlugin.success(t('taskCenter.messages.retryAllStarted', { count: result.retried || 0 }))
    await refreshAll()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.retryFailed'))
  } finally {
    bulkLoading.value = false
  }
}

async function cancelOne(row: TaskJob) {
  if (!row.capabilities?.can_cancel) return
  try {
    await cancelTask(row.job_id)
    MessagePlugin.success(t('taskCenter.messages.cancelled'))
    await refreshAll()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.cancelFailed'))
  }
}

async function deleteRecords() {
  bulkLoading.value = true
  try {
    const result = await deleteTaskRecords(30)
    MessagePlugin.success(t('taskCenter.messages.deleted', { count: result.deleted || 0 }))
    await refreshAll()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('taskCenter.messages.deleteFailed'))
  } finally {
    bulkLoading.value = false
  }
}

function taskTitle(row: TaskJob) {
  return row.display_name || row.scope_id || row.job_id
}

function stateLabel(state: TaskJobState | string) {
  return t(`taskCenter.state.${state}`) || state
}

function executionStateLabel(state: TaskExecutionState | string) {
  return t(`taskCenter.executionState.${state}`) || state
}

function kindLabel(kind: string) {
  return t(`taskCenter.kind.${kind}`) || kind
}

function stateTheme(state: TaskJobState | string) {
  if (state === 'succeeded') return 'success'
  if (state === 'failed' || state === 'canceled') return 'danger'
  if (state === 'processing' || state === 'finalizing') return 'primary'
  return 'warning'
}

function executionTheme(state: TaskExecutionState | string) {
  if (state === 'succeeded') return 'success'
  if (state === 'failed' || state === 'canceled') return 'danger'
  if (state === 'active' || state === 'retrying') return 'primary'
  return 'warning'
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString()
}

function formatJson(value: unknown) {
  try {
    return JSON.stringify(value || {}, null, 2)
  } catch {
    return '{}'
  }
}

function onVisibilityChange() {
  if (document.hidden) return
  void refreshAll()
}

onMounted(() => {
  void refreshAll()
  pollTimer = window.setInterval(() => {
    if (!document.hidden) void refreshAll()
  }, 5000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onUnmounted(() => {
  if (pollTimer) window.clearInterval(pollTimer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>

<style scoped lang="less">
.task-center {
  min-height: 100%;
  padding: 28px 32px;
  background: #f6f8fb;
  color: #1f2937;
}

.task-center__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;

  h1 {
    margin: 0;
    font-size: 24px;
    font-weight: 700;
  }

  p {
    margin: 6px 0 0;
    color: #667085;
    font-size: 14px;
  }
}

.task-center__toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.task-center__search {
  width: 260px;
}

.task-center__kind {
  width: 168px;
}

.task-center__summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 18px;
}

.summary-card {
  border: 1px solid #e4e7ec;
  border-radius: 8px;
  background: #fff;
  padding: 16px;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;

  &:hover,
  &--active {
    border-color: #19a57a;
    box-shadow: 0 6px 18px rgba(16, 24, 40, 0.08);
  }

  strong {
    display: block;
    margin-top: 8px;
    font-size: 28px;
    line-height: 1;
  }
}

.summary-card__label {
  font-size: 13px;
  color: #667085;
}

.summary-card--queued strong { color: #b7791f; }
.summary-card--processing strong { color: #2563eb; }
.summary-card--succeeded strong { color: #16835f; }
.summary-card--failed strong { color: #c2410c; }

.task-center__content {
  background: #fff;
  border: 1px solid #e4e7ec;
  border-radius: 8px;
}

.task-center__tabs {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 16px;
  border-bottom: 1px solid #edf0f5;
}

.task-center__bulk {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.task-table {
  padding: 12px 16px 16px;
}

.task-link {
  border: 0;
  background: transparent;
  padding: 0;
  color: inherit;
  display: flex;
  flex-direction: column;
  gap: 3px;
  text-align: left;
  cursor: pointer;

  span {
    font-weight: 600;
    color: #1f2937;
  }

  small {
    color: #8a95a6;
    font-size: 12px;
  }
}

.task-error {
  display: inline-block;
  max-width: 100%;
  color: #b42318;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.muted {
  color: #98a2b3;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.task-empty {
  padding: 40px 0;
}

.task-table__footer {
  display: flex;
  justify-content: flex-end;
  padding-top: 12px;
}

.task-detail {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.task-detail__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;

  h2 {
    margin: 0;
    font-size: 20px;
  }

  p {
    margin: 4px 0 0;
    color: #667085;
    font-size: 12px;
    word-break: break-all;
  }
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin: 0;

  div {
    border: 1px solid #edf0f5;
    border-radius: 8px;
    padding: 12px;
  }

  dt {
    color: #667085;
    font-size: 12px;
  }

  dd {
    margin: 6px 0 0;
    color: #1f2937;
    word-break: break-all;
  }
}

.detail-section {
  h3 {
    margin: 0 0 8px;
    font-size: 15px;
  }

  pre {
    margin: 0;
    max-height: 240px;
    overflow: auto;
    border: 1px solid #edf0f5;
    border-radius: 8px;
    background: #f8fafc;
    padding: 12px;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
  }
}

@media (max-width: 900px) {
  .task-center {
    padding: 20px 16px;
  }

  .task-center__header,
  .task-center__tabs {
    flex-direction: column;
    align-items: stretch;
  }

  .task-center__summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .task-center__search,
  .task-center__kind {
    width: 100%;
  }
}
</style>
