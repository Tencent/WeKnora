<template>
  <div class="runtime-queues">
    <header class="section-header rq-header">
      <div class="rq-title-block">
        <h2>{{ t('system.globalSettings.runtime.title') }}</h2>
        <p class="section-description">{{ t('system.globalSettings.runtime.description') }}</p>
      </div>
      <div class="rq-actions">
        <label class="rq-auto-refresh">
          <span class="rq-live-dot" :class="{ 'rq-live-dot--active': autoRefresh }" />
          <span>{{ t('system.globalSettings.runtime.autoRefresh') }}</span>
          <t-switch
            v-model="autoRefresh"
            size="small"
            :aria-label="t('system.globalSettings.runtime.autoRefresh')"
          />
        </label>
        <t-button
          variant="outline"
          size="small"
          :loading="loading"
          :disabled="loading"
          @click="reload"
        >
          <template #icon><t-icon name="refresh" /></template>
          {{ t('system.globalSettings.runtime.refresh') }}
        </t-button>
      </div>
    </header>

    <div v-if="loading && !loadedOnce" class="rq-loading" aria-live="polite">
      <div class="rq-loading-metrics">
        <t-skeleton
          v-for="n in 4"
          :key="n"
          animation="gradient"
          :row-col="[{ width: '42%', height: '28px' }, { width: '66%', height: '14px' }]"
        />
      </div>
      <t-skeleton
        animation="gradient"
        :row-col="[
          { width: '100%', height: '42px' },
          { width: '100%', height: '48px' },
          { width: '100%', height: '48px' },
          { width: '100%', height: '48px' },
        ]"
      />
    </div>

    <div v-else-if="error" class="rq-state rq-state--error" role="alert">
      <div class="rq-state-icon"><t-icon name="error-circle" size="24px" /></div>
      <div class="rq-state-copy">
        <strong>{{ t('system.globalSettings.runtime.errors.generic') }}</strong>
        <span>{{ error }}</span>
      </div>
      <t-button size="small" variant="outline" @click="reload">
        {{ t('system.globalSettings.runtime.retry') }}
      </t-button>
    </div>

    <div v-else-if="!available && !modelLimiterAvailable" class="rq-state">
      <div class="rq-state-icon"><t-icon name="info-circle" size="24px" /></div>
      <div class="rq-state-copy">
        <strong>{{ t('system.globalSettings.runtime.unavailableTitle') }}</strong>
        <span>{{ t('system.globalSettings.runtime.unavailable') }}</span>
      </div>
    </div>

    <template v-else>
      <template v-if="available">
      <section class="rq-overview" :aria-label="t('system.globalSettings.runtime.summary.title')">
        <div class="rq-overview-title">
          <span class="rq-overview-mark"><t-icon name="chart-line" /></span>
          <span>{{ t('system.globalSettings.runtime.summary.title') }}</span>
        </div>
        <div class="rq-overview-metrics">
          <div class="rq-metric rq-metric--active">
            <span class="rq-metric-label">{{ t('system.globalSettings.runtime.summary.active') }}</span>
            <strong class="rq-metric-value">{{ totalActive }}</strong>
          </div>
          <div class="rq-metric">
            <span class="rq-metric-label">{{ t('system.globalSettings.runtime.summary.pending') }}</span>
            <strong class="rq-metric-value">{{ totalPending }}</strong>
          </div>
          <div class="rq-metric" :class="{ 'rq-metric--warning': totalRetry > 0 }">
            <span class="rq-metric-label">{{ t('system.globalSettings.runtime.summary.retry') }}</span>
            <strong class="rq-metric-value">{{ totalRetry }}</strong>
          </div>
          <div class="rq-metric" :class="{ 'rq-metric--danger': totalArchived > 0 }">
            <span class="rq-metric-label">{{ t('system.globalSettings.runtime.summary.archived') }}</span>
            <strong class="rq-metric-value">{{ totalArchived }}</strong>
          </div>
        </div>
      </section>

      <section class="rq-pools">
        <div class="rq-pools-header">
          <div>
            <h3 class="rq-section-title">{{ t('system.globalSettings.runtime.poolsTitle') }}</h3>
            <p>{{ t('system.globalSettings.runtime.poolsDescription') }}</p>
          </div>
          <span class="rq-pools-note">{{ t('system.globalSettings.runtime.perInstance') }}</span>
        </div>
        <div class="rq-pool-grid">
          <div v-for="pool in pools" :key="pool.name" class="rq-pool-card">
            <div class="rq-pool-topline">
              <span class="rq-pool-name">{{ poolLabel(pool.name) }}</span>
              <strong class="rq-pool-value">
                {{ pool.instances > 0 ? `${pool.active}/${pool.cluster_capacity}` : pool.concurrency }}
              </strong>
            </div>
            <p class="rq-pool-desc">
              {{ poolDescription(pool.name) }}
              <span class="rq-pool-meta">
                {{ t('system.globalSettings.runtime.poolConfigured', { value: pool.concurrency }) }}
                <template v-if="pool.instances > 0">
                  · {{ t('system.globalSettings.runtime.poolInstances', { value: pool.instances }) }}
                  · {{ t('system.globalSettings.runtime.poolUtilization', { value: poolUtilization(pool) }) }}
                </template>
                · {{ t('system.globalSettings.runtime.queueCount', { value: pool.queue_count }) }}
              </span>
            </p>
          </div>
        </div>
      </section>

      <section class="rq-details">
        <div class="rq-details-header">
          <div>
            <h3 class="rq-section-title">{{ t('system.globalSettings.runtime.detailsTitle') }}</h3>
            <p>{{ t('system.globalSettings.runtime.detailsDescription') }}</p>
          </div>
          <span v-if="updatedAt" class="rq-updated-at">
            <t-icon name="time" />
            {{ t('system.globalSettings.runtime.updatedAt', { value: updatedAt }) }}
          </span>
        </div>

        <div v-if="totalArchived > 0" class="rq-failed-notice" role="status">
          <span class="rq-failed-notice__icon" aria-hidden="true">
            <t-icon name="error-circle" />
          </span>
          <div class="rq-failed-notice__text">
            <p class="rq-failed-notice__title">
              {{ t('system.globalSettings.runtime.failedNotice.title', { count: totalArchived }) }}
            </p>
            <p class="rq-failed-notice__desc">
              {{ t('system.globalSettings.runtime.failedNotice.description') }}
            </p>
          </div>
        </div>

        <div v-if="queues.length === 0" class="rq-empty">
          <t-icon name="queue" size="28px" />
          <span>{{ t('system.globalSettings.runtime.empty') }}</span>
        </div>

        <div v-else class="data-table-shell rq-table-shell">
          <t-table
            row-key="name"
            :data="queues"
            :columns="columns"
            size="medium"
            hover
          >
            <template #name="{ row }">
              <div class="rq-queue-cell">
                <span class="rq-queue-name">{{ queueLabel(row.name) }}</span>
                <span class="rq-queue-meta">{{ queueMeta(row) }}</span>
              </div>
            </template>
            <template #active="{ row }">
              <span class="rq-number" :class="{ 'rq-number--active': row.active > 0 }">{{ row.active }}</span>
            </template>
            <template #pending="{ row }">
              <div class="rq-backlog">
                <span class="rq-number" :class="{ 'rq-number--active': row.pending > 0 }">{{ row.pending }}</span>
                <small v-if="row.scheduled > 0">
                  +{{ row.scheduled }} {{ t('system.globalSettings.runtime.columns.scheduled') }}
                </small>
              </div>
            </template>
            <template #retry="{ row }">
              <span class="rq-number" :class="{ 'rq-number--warning': row.retry > 0 }">{{ row.retry }}</span>
            </template>
            <template #archived="{ row }">
              <t-button
                v-if="row.archived > 0"
                variant="text"
                theme="danger"
                size="small"
                class="rq-failed-count"
                :aria-label="t('system.globalSettings.runtime.failedTasks.openAria', { queue: queueLabel(row.name), count: row.archived })"
                @click="openFailedTasks(row)"
              >
                {{ row.archived }}<t-icon name="chevron-right" />
              </t-button>
              <span v-else class="rq-number">0</span>
            </template>
            <template #latency_ms="{ row }">
              <span class="rq-latency">{{ formatLatency(row.latency_ms) }}</span>
            </template>
            <template #status="{ row }">
              <span class="rq-status" :class="`rq-status--${queueState(row).tone}`">
                <i />{{ queueState(row).label }}
              </span>
            </template>
          </t-table>
        </div>
      </section>
      </template>

      <section class="rq-details rq-models">
        <div class="rq-details-header">
          <div>
            <h3 class="rq-section-title">{{ t('system.globalSettings.runtime.models.title') }}</h3>
            <p>{{ t('system.globalSettings.runtime.models.description') }}</p>
          </div>
          <span class="rq-pools-note">{{ t('system.globalSettings.runtime.models.scope') }}</span>
        </div>
        <div v-if="!modelLimiterAvailable" class="rq-empty">
          <t-icon name="info-circle" size="28px" />
          <span>{{ t('system.globalSettings.runtime.models.disabled') }}</span>
        </div>
        <div v-else-if="models.length === 0" class="rq-empty">
          <t-icon name="server" size="28px" />
          <span>{{ t('system.globalSettings.runtime.models.empty') }}</span>
        </div>
        <div v-else class="data-table-shell rq-table-shell">
          <t-table row-key="model_id" :data="models" :columns="modelColumns" size="medium" hover>
            <template #model_id="{ row }">
              <div class="rq-queue-cell">
                <span class="rq-queue-name">{{ row.name || row.model_id }}</span>
                <span class="rq-queue-meta">{{ row.name ? row.model_id : t('system.globalSettings.runtime.models.backgroundOnly') }}</span>
              </div>
            </template>
            <template #active="{ row }"><span class="rq-number" :class="{ 'rq-number--active': row.active > 0 }">{{ row.active }}</span></template>
            <template #waiting="{ row }"><span class="rq-number" :class="{ 'rq-number--warning': row.waiting > 0 }">{{ row.waiting }}</span></template>
            <template #usage="{ row }">
              <div class="rq-model-usage">
                <t-progress :percentage="modelUsage(row)" size="small" :label="false" />
                <span>{{ row.active }} / {{ row.limit }}</span>
              </div>
            </template>
            <template #status="{ row }">
              <span class="rq-status" :class="`rq-status--${modelState(row).tone}`"><i />{{ modelState(row).label }}</span>
            </template>
          </t-table>
        </div>
      </section>

      <p class="rq-footnote">{{ t('system.globalSettings.runtime.footnote') }}</p>
    </template>

    <SettingDrawer
      v-model:visible="failedTasksVisible"
      class="rq-failed-drawer"
      :title="t('system.globalSettings.runtime.failedTasks.title', { queue: failedTaskQueueLabel })"
      :description="t('system.globalSettings.runtime.failedTasks.description')"
      icon="error-circle"
      width="680px"
      :min-width="520"
      :max-width="960"
      storage-key="setting-drawer:width:runtime-failed-tasks"
      hide-footer
    >
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">
          {{ t('system.globalSettings.runtime.failedTasks.guideTitle') }}
        </h4>
        <p class="rq-failed-guide-desc">
          {{ t('system.globalSettings.runtime.failedTasks.guideDescription') }}
        </p>
      </section>

      <section class="setting-drawer__section">
        <div class="rq-failed-section-head">
          <h4 class="setting-drawer__section-title">
            {{ t('system.globalSettings.runtime.failedTasks.listTitle') }}
          </h4>
          <t-button
            variant="text"
            size="small"
            :loading="failedTasksLoading && !failedTasksLoadingMore"
            @click="reloadFailedTasks"
          >
            <template #icon><t-icon name="refresh" /></template>
            {{ t('system.globalSettings.runtime.refresh') }}
          </t-button>
        </div>

        <div v-if="failedTasksLoading && failedTasks.length === 0" class="rq-failed-loading">
          <t-loading size="small" />
          <span>{{ t('system.globalSettings.runtime.loading') }}</span>
        </div>
        <div v-else-if="failedTasksError" class="rq-failed-error-state">
          <span>{{ failedTasksError }}</span>
          <t-button size="small" variant="outline" @click="reloadFailedTasks">
            {{ t('system.globalSettings.runtime.retry') }}
          </t-button>
        </div>
        <t-empty
          v-else-if="failedTasks.length === 0"
          :description="t('system.globalSettings.runtime.failedTasks.empty')"
        />
        <div v-else class="rq-failed-list-panel">
          <article
            v-for="task in failedTasks"
            :key="task.id"
            class="rq-failed-row"
          >
            <div class="rq-failed-row-content">
              <div class="rq-failed-row-summary">
                <span class="rq-failed-row-type">{{ failedTaskTypeLabel(task.type) }}</span>
                <span class="rq-failed-row-sep" aria-hidden="true">·</span>
                <span class="rq-failed-row-stat">
                  {{ t('system.globalSettings.runtime.failedTasks.attempts', { count: task.retried + 1 }) }}
                </span>
                <span class="rq-failed-row-sep" aria-hidden="true">·</span>
                <time :datetime="task.last_failed_at">{{ formatFailedAt(task.last_failed_at) }}</time>
              </div>
              <dl v-if="failedTaskRefs(task).length > 0" class="rq-failed-row-refs">
                <div v-for="ref in failedTaskRefs(task)" :key="ref.key" class="rq-failed-ref">
                  <dt>{{ ref.label }}</dt>
                  <dd :title="ref.value">{{ ref.value }}</dd>
                </div>
              </dl>
              <p v-else class="rq-failed-row-unknown">
                {{ t('system.globalSettings.runtime.failedTasks.unknownTarget') }}
              </p>
              <p class="rq-failed-row-error">
                {{ task.last_error || t('system.globalSettings.runtime.failedTasks.noError') }}
              </p>
            </div>

            <div class="rq-failed-row-actions">
              <t-popconfirm
                theme="warning"
                :content="t('system.globalSettings.runtime.failedTasks.retryConfirm')"
                @confirm="retryFailedTask(task)"
              >
                <t-button
                  shape="square"
                  variant="text"
                  size="small"
                  class="rq-failed-icon-btn"
                  :title="t('system.globalSettings.runtime.failedTasks.retryOnce')"
                  :aria-label="t('system.globalSettings.runtime.failedTasks.retryOnce')"
                  :loading="failedTaskActionID === task.id && failedTaskAction === 'retry'"
                  :disabled="Boolean(failedTaskActionID)"
                >
                  <t-icon name="refresh" />
                </t-button>
              </t-popconfirm>
              <t-popconfirm
                theme="danger"
                :content="t('system.globalSettings.runtime.failedTasks.deleteConfirm')"
                @confirm="removeFailedTask(task)"
              >
                <t-button
                  shape="square"
                  variant="text"
                  size="small"
                  theme="danger"
                  class="rq-failed-icon-btn"
                  :title="t('system.globalSettings.runtime.failedTasks.deleteRecord')"
                  :aria-label="t('system.globalSettings.runtime.failedTasks.deleteRecord')"
                  :loading="failedTaskActionID === task.id && failedTaskAction === 'delete'"
                  :disabled="Boolean(failedTaskActionID)"
                >
                  <t-icon name="delete" />
                </t-button>
              </t-popconfirm>
            </div>
          </article>

          <div ref="failedTasksSentinelRef" class="rq-failed-load-sentinel" aria-hidden="true" />

          <div class="rq-failed-list-footer">
            <span class="rq-failed-list-status">
              <template v-if="failedTasksLoadingMore">
                {{ t('system.globalSettings.runtime.failedTasks.loadingMore') }}
              </template>
              <template v-else-if="!failedTasksHasMore">
                {{ t('system.globalSettings.runtime.failedTasks.loadedAll', { count: failedTasks.length }) }}
              </template>
              <template v-else>
                {{ t('system.globalSettings.runtime.failedTasks.loadedSummary', { count: failedTasks.length }) }}
              </template>
            </span>
            <t-button
              v-if="failedTasksHasMore"
              variant="outline"
              block
              :loading="failedTasksLoadingMore"
              @click="loadMoreFailedTasks"
            >
              {{ t('system.globalSettings.runtime.failedTasks.loadMore') }}
            </t-button>
          </div>
        </div>
      </section>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  deleteRuntimeFailedTask,
  getRuntimeFailedTasks,
  getRuntimeQueues,
  retryRuntimeFailedTask,
  type ModelRuntimeStat,
  type QueueStat,
  type RuntimeFailedTask,
  type RuntimeWorkerPool,
} from '@/api/system'

const { t, te, locale } = useI18n()

const POLL_INTERVAL_MS = 5000

const queues = ref<QueueStat[]>([])
const pools = ref<RuntimeWorkerPool[]>([])
const models = ref<ModelRuntimeStat[]>([])
const modelLimiterAvailable = ref(false)
const available = ref(true)
const loading = ref(false)
const loadedOnce = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const updatedAt = ref('')
const failedTasksVisible = ref(false)
const failedTaskQueue = ref<QueueStat | null>(null)
const failedTasks = ref<RuntimeFailedTask[]>([])
const failedTasksLoading = ref(false)
const failedTasksLoadingMore = ref(false)
const failedTasksError = ref('')
const failedTasksPage = ref(1)
const failedTasksHasMore = ref(false)
const failedTasksSentinelRef = ref<HTMLElement | null>(null)
const failedTaskActionID = ref('')
const failedTaskAction = ref<'retry' | 'delete' | ''>('')

const FAILED_TASK_PAGE_SIZE = 20
const failedTaskTypeKeys: Record<string, string> = {
  'document:process': 'documentProcess',
  'manual:process': 'manualProcess',
  'knowledge:post_process': 'postProcess',
  'summary:generation': 'summary',
  'datatable:summary': 'tableSummary',
  'question:generation': 'question',
  'image:multimodal': 'multimodal',
  'chunk:extract': 'graph',
  'datasource:sync': 'sync',
  'faq:import': 'faqImport',
  'knowledge:list_reparse': 'batchReparse',
  'knowledge:list_delete': 'batchDelete',
  'knowledge:move': 'move',
  'index:delete': 'indexDelete',
  'kb:clone': 'kbClone',
  'kb:delete': 'kbDelete',
  'wiki:ingest': 'wikiIngest',
  'wiki:finalize': 'wikiFinalize',
}

let pollTimer: ReturnType<typeof setInterval> | null = null
let failedTasksScrollObserver: IntersectionObserver | null = null

const columns = computed(() => [
  { colKey: 'name', title: t('system.globalSettings.runtime.columns.queue'), minWidth: 188 },
  { colKey: 'active', title: t('system.globalSettings.runtime.columns.active'), width: 74, align: 'center' as const },
  { colKey: 'pending', title: t('system.globalSettings.runtime.columns.pending'), width: 84, align: 'center' as const },
  { colKey: 'retry', title: t('system.globalSettings.runtime.columns.retry'), width: 68, align: 'center' as const },
  { colKey: 'archived', title: t('system.globalSettings.runtime.columns.archived'), width: 96, align: 'center' as const },
  { colKey: 'latency_ms', title: t('system.globalSettings.runtime.columns.latency'), width: 104, align: 'center' as const },
  { colKey: 'status', title: t('system.globalSettings.runtime.columns.status'), width: 96 },
])
const modelColumns = computed(() => [
  { colKey: 'model_id', title: t('system.globalSettings.runtime.models.columns.model'), minWidth: 240 },
  { colKey: 'active', title: t('system.globalSettings.runtime.models.columns.active'), width: 86, align: 'center' as const },
  { colKey: 'waiting', title: t('system.globalSettings.runtime.models.columns.waiting'), width: 86, align: 'center' as const },
  { colKey: 'usage', title: t('system.globalSettings.runtime.models.columns.usage'), width: 190 },
  { colKey: 'status', title: t('system.globalSettings.runtime.columns.status'), width: 96 },
])

function modelUsage(row: ModelRuntimeStat): number {
  return row.limit > 0 ? Math.min(100, Math.round(row.active / row.limit * 100)) : 0
}

function modelState(row: ModelRuntimeStat): { label: string; tone: string } {
  if (row.waiting > 0) return { label: t('system.globalSettings.runtime.models.status.queued'), tone: 'attention' }
  if (row.active >= row.limit) return { label: t('system.globalSettings.runtime.models.status.full'), tone: 'waiting' }
  if (row.active > 0) return { label: t('system.globalSettings.runtime.status.working'), tone: 'working' }
  return { label: t('system.globalSettings.runtime.status.idle'), tone: 'idle' }
}

const totalActive = computed(() => queues.value.reduce((s, q) => s + q.active, 0))
const totalPending = computed(() => queues.value.reduce((s, q) => s + q.pending, 0))
const totalRetry = computed(() => queues.value.reduce((s, q) => s + q.retry, 0))
const totalArchived = computed(() => queues.value.reduce((s, q) => s + q.archived, 0))
const failedTaskQueueLabel = computed(() => failedTaskQueue.value ? queueLabel(failedTaskQueue.value.name) : '')

// Friendly per-queue label lives in i18n; falls back to the raw queue
// name so a queue added on the backend still renders before translations
// catch up.
function queueLabel(name: string): string {
  const path = `system.globalSettings.runtime.queueNames.${name}`
  return te(path) ? (t(path) as string) : name
}

function queueDescription(name: string): string {
  const path = `system.globalSettings.runtime.queueDescriptions.${name}`
  return te(path) ? (t(path) as string) : name
}

function queueMeta(row: QueueStat): string {
  const scope = queueDescription(row.name)
  if (poolQueueCount(row.pool) > 1) {
    return `${scope} · ${t('system.globalSettings.runtime.weightShort', { value: row.weight })}`
  }
  return scope
}

function failedTaskTypeLabel(type: string): string {
  const key = failedTaskTypeKeys[type]
  if (!key) return type
  const path = `system.globalSettings.runtime.failedTasks.taskTypes.${key}`
  return te(path) ? (t(path) as string) : type
}

interface FailedTaskRef {
  key: string
  label: string
  value: string
}

function failedTaskRefs(task: RuntimeFailedTask): FailedTaskRef[] {
  const refs: FailedTaskRef[] = []
  if (task.knowledge_base_id) {
    refs.push({
      key: 'kb',
      label: t('system.globalSettings.runtime.failedTasks.knowledgeBaseLabel'),
      value: task.knowledge_base_id,
    })
  }
  if (task.knowledge_id) {
    refs.push({
      key: 'knowledge',
      label: t('system.globalSettings.runtime.failedTasks.knowledgeLabel'),
      value: task.knowledge_id,
    })
  }
  if (task.task_id) {
    refs.push({
      key: 'task',
      label: t('system.globalSettings.runtime.failedTasks.taskIDLabel'),
      value: task.task_id,
    })
  }
  if (refs.length === 0 && task.tenant_id) {
    refs.push({
      key: 'tenant',
      label: t('system.globalSettings.runtime.failedTasks.tenantLabel'),
      value: String(task.tenant_id),
    })
  }
  return refs
}

function formatFailedAt(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(locale.value, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

function poolLabel(pool: string): string {
  const path = `system.globalSettings.runtime.pools.${pool}`
  return te(path) ? (t(path) as string) : pool
}

function poolDescription(pool: string): string {
  const path = `system.globalSettings.runtime.poolDescriptions.${pool}`
  return te(path) ? (t(path) as string) : pool
}

function poolQueueCount(pool: string): number {
  return pools.value.find((item) => item.name === pool)?.queue_count ?? 0
}

function poolUtilization(pool: RuntimeWorkerPool): number {
  return Math.round(Math.max(0, Math.min(1, pool.utilization || 0)) * 100)
}

function formatLatency(ms: number): string {
  if (!ms || ms <= 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)} s`
  const m = Math.floor(s / 60)
  const rem = Math.round(s % 60)
  return `${m}m ${rem}s`
}

function queueState(row: QueueStat): { label: string; tone: string } {
  if (row.paused) {
    return { label: t('system.globalSettings.runtime.status.paused'), tone: 'paused' }
  }
  if (row.archived > 0) {
    return { label: t('system.globalSettings.runtime.status.actionRequired'), tone: 'danger' }
  }
  if (row.retry > 0) {
    return { label: t('system.globalSettings.runtime.status.retrying'), tone: 'attention' }
  }
  if (row.active > 0) {
    return { label: t('system.globalSettings.runtime.status.working'), tone: 'working' }
  }
  if (row.pending > 0 || row.scheduled > 0) {
    return { label: t('system.globalSettings.runtime.status.waiting'), tone: 'waiting' }
  }
  return { label: t('system.globalSettings.runtime.status.idle'), tone: 'idle' }
}

async function fetchFailedTasks(reset: boolean) {
  const queue = failedTaskQueue.value?.name
  if (!queue) return
  if (!reset && (failedTasksLoadingMore.value || !failedTasksHasMore.value)) return

  const page = reset ? 1 : failedTasksPage.value + 1
  if (reset) {
    failedTasksPage.value = 1
    failedTasks.value = []
    failedTasksLoading.value = true
  } else {
    failedTasksLoadingMore.value = true
  }
  failedTasksError.value = ''
  try {
    const response = await getRuntimeFailedTasks(queue, page, FAILED_TASK_PAGE_SIZE)
    if (!response.available) {
      failedTasksError.value = t('system.globalSettings.runtime.failedTasks.unavailable')
      return
    }
    failedTasks.value = reset ? response.tasks : [...failedTasks.value, ...response.tasks]
    failedTasksPage.value = page
    failedTasksHasMore.value = response.has_more
  } catch (err: any) {
    failedTasksError.value = err?.message || t('system.globalSettings.runtime.failedTasks.loadError')
  } finally {
    if (reset) {
      failedTasksLoading.value = false
    } else {
      failedTasksLoadingMore.value = false
    }
    await nextTick()
    attachFailedTasksScrollObserver()
  }
}

function detachFailedTasksScrollObserver() {
  failedTasksScrollObserver?.disconnect()
  failedTasksScrollObserver = null
}

function attachFailedTasksScrollObserver() {
  detachFailedTasksScrollObserver()
  const sentinel = failedTasksSentinelRef.value
  if (!sentinel || !failedTasksVisible.value || !failedTasksHasMore.value) return
  const root = sentinel.closest('.t-drawer__body') as HTMLElement | null
  if (!root) return
  failedTasksScrollObserver = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        void loadMoreFailedTasks()
      }
    },
    { root, rootMargin: '96px 0px', threshold: 0 },
  )
  failedTasksScrollObserver.observe(sentinel)
}

function openFailedTasks(row: QueueStat) {
  failedTaskQueue.value = row
  failedTasksVisible.value = true
  fetchFailedTasks(true)
}

function reloadFailedTasks() {
  return fetchFailedTasks(true)
}

function loadMoreFailedTasks() {
  if (failedTasksLoading.value || failedTasksLoadingMore.value || !failedTasksHasMore.value) return
  return fetchFailedTasks(false)
}

async function retryFailedTask(task: RuntimeFailedTask) {
  const queue = failedTaskQueue.value?.name
  if (!queue) return
  failedTaskActionID.value = task.id
  failedTaskAction.value = 'retry'
  try {
    await retryRuntimeFailedTask(queue, task.id)
    MessagePlugin.success(t('system.globalSettings.runtime.failedTasks.retrySuccess'))
    await Promise.all([reloadFailedTasks(), load(false)])
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('system.globalSettings.runtime.failedTasks.retryError'))
  } finally {
    failedTaskActionID.value = ''
    failedTaskAction.value = ''
  }
}

async function removeFailedTask(task: RuntimeFailedTask) {
  const queue = failedTaskQueue.value?.name
  if (!queue) return
  failedTaskActionID.value = task.id
  failedTaskAction.value = 'delete'
  try {
    await deleteRuntimeFailedTask(queue, task.id)
    MessagePlugin.success(t('system.globalSettings.runtime.failedTasks.deleteSuccess'))
    await Promise.all([reloadFailedTasks(), load(false)])
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('system.globalSettings.runtime.failedTasks.deleteError'))
  } finally {
    failedTaskActionID.value = ''
    failedTaskAction.value = ''
  }
}

async function load(showSpinner: boolean) {
  if (showSpinner) loading.value = true
  try {
    const resp = await getRuntimeQueues()
    available.value = resp.available
    pools.value = resp.pools || []
    queues.value = resp.queues || []
    models.value = resp.models || []
    modelLimiterAvailable.value = Boolean(resp.model_limiter_available)
    updatedAt.value = new Date((resp.timestamp || Date.now() / 1000) * 1000)
      .toLocaleTimeString(locale.value, { hour12: false })
    error.value = ''
    loadedOnce.value = true
  } catch (err: any) {
    error.value = err?.message || t('system.globalSettings.runtime.errors.generic')
  } finally {
    if (showSpinner) loading.value = false
  }
}

function reload() {
  load(true)
}

function startPolling() {
  stopPolling()
  if (!autoRefresh.value) return
  pollTimer = setInterval(() => {
    // Silent background refresh — no spinner so the table doesn't flash.
    if (!loading.value) load(false)
  }, POLL_INTERVAL_MS)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(autoRefresh, (on) => {
  if (on) startPolling()
  else stopPolling()
})

watch(failedTasksVisible, async (open) => {
  if (!open) {
    detachFailedTasksScrollObserver()
    return
  }
  await nextTick()
  attachFailedTasksScrollObserver()
}, { flush: 'post' })

watch(failedTasksHasMore, async () => {
  if (!failedTasksVisible.value) return
  await nextTick()
  attachFailedTasksScrollObserver()
})

onMounted(() => {
  load(true)
  startPolling()
})

onUnmounted(() => {
  stopPolling()
  detachFailedTasksScrollObserver()
})
</script>

<style lang="less" scoped>
.runtime-queues {
  color: var(--td-text-color-primary);
}

.rq-models {
  margin-top: 40px;
  padding-top: 32px;
}

.rq-model-usage {
  display: grid;
  grid-template-columns: minmax(72px, 1fr) auto;
  align-items: center;
  gap: 10px;
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}

.rq-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 24px;

  h2 {
    margin: 0 0 8px;
    color: var(--td-text-color-primary);
    font-size: 22px;
    font-weight: 600;
    line-height: 1.3;
    letter-spacing: -0.01em;
  }

  .section-description {
    max-width: 560px;
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 14px;
    line-height: 1.6;
    text-wrap: pretty;
  }
}

.rq-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.rq-auto-refresh {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 32px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  white-space: nowrap;
  cursor: pointer;
}

.rq-live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);
  transition: background-color 0.2s ease, box-shadow 0.2s ease;

  &--active {
    background: var(--td-success-color);
    box-shadow: 0 0 0 3px var(--td-success-color-1);
  }
}

.rq-loading {
  display: grid;
  gap: 22px;
  padding-top: 4px;
}

.rq-loading-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1px;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-component-stroke);

  :deep(.t-skeleton) {
    padding: 18px;
    background: var(--td-bg-color-container);
  }
}

.rq-state {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px;
  min-height: 112px;
  padding: 20px 22px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
}

.rq-state-icon {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border-radius: 8px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-container);
}

.rq-state-copy {
  display: flex;
  flex-direction: column;
  gap: 5px;

  strong {
    font-size: 14px;
    font-weight: 600;
  }

  span {
    max-width: 560px;
    color: var(--td-text-color-secondary);
    font-size: 14px;
    line-height: 1.55;
  }
}

.rq-state--error .rq-state-icon {
  color: var(--td-error-color);
  background: var(--td-error-color-1);
}

.rq-overview {
  display: flex;
  min-height: 64px;
  align-items: center;
  gap: 28px;
  margin-bottom: 30px;
  padding: 13px 16px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.rq-overview-title {
  display: inline-flex;
  min-width: 112px;
  align-items: center;
  gap: 9px;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
}

.rq-overview-mark {
  display: grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border-radius: 6px;
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
  font-size: 15px;
}

.rq-overview-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(64px, 1fr));
  align-items: stretch;
  gap: 24px;
  flex: 1;
}

.rq-metric {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
}

.rq-metric-value {
  color: var(--td-text-color-primary);
  font-size: 20px;
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}

.rq-metric-label {
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.35;
  white-space: nowrap;
}

.rq-metric--warning .rq-metric-value {
  color: var(--td-warning-color);
}

.rq-metric--danger .rq-metric-value {
  color: var(--td-error-color);
}

.rq-pools {
  margin-bottom: 30px;
}

.rq-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 6px;
  color: var(--td-text-color-primary);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  user-select: none;

  &::before {
    content: '';
    flex-shrink: 0;
    width: 3px;
    height: 15px;
    border-radius: 2px;
    background: var(--td-brand-color);
  }
}

.rq-pools-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 14px;

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
  }
}

.rq-pools-note {
  flex-shrink: 0;
  margin-top: 2px;
  padding: 4px 10px;
  border-radius: 999px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  background: var(--td-bg-color-secondarycontainer);
}

.rq-pool-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.rq-pool-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 8px;
  padding: 16px 18px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.rq-pool-topline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.rq-pool-name {
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 500;
  line-height: 1.35;
}

.rq-pool-value {
  color: var(--td-brand-color);
  font-size: 22px;
  font-weight: 600;
  line-height: 1;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}

.rq-pool-desc {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.55;
  text-wrap: pretty;
}

.rq-pool-meta {
  display: block;
  margin-top: 4px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.4;
}

.rq-details {
  margin-top: 2px;
}

.rq-details-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 14px;

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
  }
}

.rq-updated-at {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.rq-failed-notice {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 14px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.rq-failed-notice__icon {
  display: grid;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  place-items: center;
  border-radius: 8px;
  color: var(--td-error-color);
  font-size: 16px;
  background: color-mix(in srgb, var(--td-error-color) 10%, transparent);
}

.rq-failed-notice__text {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 2px;
}

.rq-failed-notice__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.45;
  font-variant-numeric: tabular-nums;
}

.rq-failed-notice__desc {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.rq-failed-guide-desc {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.rq-failed-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 4px;

  .setting-drawer__section-title {
    margin-bottom: 0;
  }
}

.rq-failed-loading {
  display: flex;
  min-height: 180px;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.rq-failed-error-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.55;
  background: var(--td-bg-color-container);
}

.rq-empty {
  display: flex;
  min-height: 180px;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 10px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 10px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.rq-queue-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.rq-queue-name {
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 500;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rq-queue-meta {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.45;
}

.rq-number,
.rq-latency {
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

.rq-number--active {
  color: var(--td-brand-color);
  font-weight: 600;
}

.rq-number--warning {
  color: var(--td-warning-color);
  font-weight: 600;
}

.rq-number--danger {
  color: var(--td-error-color);
  font-weight: 600;
}

.rq-failed-count {
  min-width: 0;
  height: 28px;
  padding: 0 2px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;

  :deep(.t-button__text) {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }
}

.rq-backlog {
  display: flex;
  align-items: center;
  flex-direction: column;
  gap: 1px;

  small {
    color: var(--td-text-color-placeholder);
    font-size: 11px;
    white-space: nowrap;
  }
}

.rq-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  white-space: nowrap;

  i {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--td-text-color-placeholder);
  }

  &--working i {
    background: var(--td-brand-color);
  }

  &--attention i,
  &--paused i {
    background: var(--td-warning-color);
  }

  &--attention,
  &--paused {
    color: var(--td-warning-color);
  }

  &--danger {
    color: var(--td-error-color);
  }

  &--danger i {
    background: var(--td-error-color);
  }
}

.data-table-shell.rq-table-shell {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    height: 40px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.01em;
    white-space: nowrap;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td) {
    height: 56px;
    padding-top: 10px;
    padding-bottom: 10px;
    font-size: 14px;
    font-variant-numeric: tabular-nums;
  }

  /* Metric columns: center short numbers under multi-char headers. */
  &:deep(.t-table th.t-align-center),
  &:deep(.t-table td.t-align-center) {
    text-align: center;
  }

  &:deep(td.t-align-center .rq-failed-count) {
    margin-inline: auto;
  }

  &:deep(.t-table__body tr:last-child td) {
    border-bottom: 0;
  }
}

.rq-footnote {
  margin: 12px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.55;
}

.rq-failed-list-panel {
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.rq-failed-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px 12px 12px 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-of-type {
    border-bottom: 0;
  }
}

.rq-failed-row-content {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 4px;
}

.rq-failed-row-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.45;
}

.rq-failed-row-type {
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.rq-failed-row-sep {
  color: var(--td-text-color-placeholder);
}

.rq-failed-row-stat,
.rq-failed-row-summary time {
  font-variant-numeric: tabular-nums;
}

.rq-failed-row-refs {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 2px 0 0;
}

.rq-failed-ref {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  gap: 8px;
  align-items: baseline;
  margin: 0;

  dt {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    line-height: 1.45;
    white-space: nowrap;
  }

  dd {
    margin: 0;
    overflow: hidden;
    color: var(--td-text-color-secondary);
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 11px;
    line-height: 1.45;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.rq-failed-row-unknown {
  margin: 2px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.45;
}

.rq-failed-row-error {
  margin: 8px 0 0;
  padding: 8px 10px;
  border-radius: 6px;
  color: var(--td-text-color-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1.55;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  background: var(--td-bg-color-secondarycontainer);
}

.rq-failed-row-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 0;
  margin-top: -2px;
}

.rq-failed-icon-btn {
  width: 28px;
  height: 28px;
}

.rq-failed-load-sentinel {
  height: 1px;
}

.rq-failed-list-footer {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 16px 14px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.rq-failed-list-status {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.5;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 860px) {
  .rq-header,
  .rq-details-header,
  .rq-pools-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .rq-loading-metrics {
    grid-template-columns: repeat(2, 1fr);
  }

  .rq-actions {
    width: 100%;
    justify-content: space-between;
  }

  .rq-overview {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .rq-overview-metrics {
    width: 100%;
    grid-template-columns: repeat(4, minmax(64px, 1fr));
  }

  .rq-pool-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 620px) {
  .rq-loading-metrics {
    grid-template-columns: 1fr;
  }

  .rq-overview-metrics {
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .rq-pool-grid {
    grid-template-columns: 1fr;
  }

  .rq-state {
    grid-template-columns: auto minmax(0, 1fr);

    .t-button {
      grid-column: 2;
      justify-self: start;
    }
  }

  .rq-failed-row {
    padding: 12px;
  }

  .rq-failed-row-summary {
    gap: 4px;
  }
}
</style>
