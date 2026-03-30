<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSyncLogs, type SyncLog } from '@/api/datasource'

const props = defineProps<{
  dataSourceId: string
  dataSourceName?: string
}>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const logs = ref<SyncLog[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const expandedId = ref('')
const pageSize = 50

async function fetchLogs(reset = true) {
  if (!props.dataSourceId) return

  if (reset) {
    loading.value = true
  } else {
    loadingMore.value = true
  }

  try {
    const offset = reset ? 0 : logs.value.length
    const res = await getSyncLogs(props.dataSourceId, pageSize, offset)
    const items = res?.data || res || []
    logs.value = reset ? items : [...logs.value, ...items]
    hasMore.value = items.length === pageSize
  } catch { /* ignore */ }

  if (reset) {
    loading.value = false
  } else {
    loadingMore.value = false
  }
}

watch(visible, (v) => {
  if (!v) return
  expandedId.value = ''
  fetchLogs(true)
})

function toggleExpand(id: string) {
  expandedId.value = expandedId.value === id ? '' : id
}

function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return
  fetchLogs(false)
}

// --- Stats ---
const stats = computed(() => {
  const total = logs.value.length
  const success = logs.value.filter(l => l.status === 'success').length
  const failed = logs.value.filter(l => l.status === 'failed').length
  const totalItems = logs.value.reduce((acc, l) => acc + (l.items_created || 0) + (l.items_updated || 0), 0)
  return { total, success, failed, totalItems }
})

// --- Helpers ---
function statusIcon(status: string) {
  switch (status) {
    case 'success': return 'check-circle-filled'
    case 'running': return 'loading'
    case 'failed': return 'close-circle-filled'
    case 'partial': return 'error-circle-filled'
    case 'canceled': return 'minus-circle-filled'
    default: return 'info-circle-filled'
  }
}

function statusColor(status: string) {
  switch (status) {
    case 'success': return 'var(--td-success-color)'
    case 'running': return 'var(--td-brand-color)'
    case 'failed': return 'var(--td-error-color)'
    case 'partial': return 'var(--td-warning-color)'
    default: return 'var(--td-text-color-placeholder)'
  }
}

function formatTime(ts: string | null) {
  if (!ts) return '--'
  const d = new Date(ts)
  if (isNaN(d.getTime())) return '--'
  return d.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

function formatDate(ts: string | null) {
  if (!ts) return ''
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function formatHourMin(ts: string | null) {
  if (!ts) return ''
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function duration(log: SyncLog) {
  if (!log.started_at || !log.finished_at) return '--'
  const ms = new Date(log.finished_at).getTime() - new Date(log.started_at).getTime()
  if (ms < 0) return '--'
  if (ms < 1000) return `<1s`
  const sec = Math.round(ms / 1000)
  if (sec < 60) return `${sec}s`
  return `${Math.floor(sec / 60)}m${sec % 60}s`
}

function hasPills(log: SyncLog) {
  return log.items_created > 0 || log.items_updated > 0 || log.items_deleted > 0 || log.items_skipped > 0 || log.items_failed > 0
}

// Group logs by date
const groupedLogs = computed(() => {
  const groups: { date: string; logs: SyncLog[] }[] = []
  let currentDate = ''
  for (const log of logs.value) {
    const d = formatDate(log.started_at)
    if (d !== currentDate) {
      currentDate = d
      groups.push({ date: d, logs: [] })
    }
    groups[groups.length - 1].logs.push(log)
  }
  return groups
})
</script>

<template>
  <t-drawer
    v-model:visible="visible"
    size="480px"
    destroy-on-close
    class="ds-logs-drawer"
  >
    <template #header>
      <div class="logs-drawer-header">
        <span class="logs-drawer-title">
          {{ props.dataSourceName ? `${t('datasource.syncHistory')} · ${props.dataSourceName}` : t('datasource.syncHistory') }}
        </span>
        <t-tooltip :content="t('datasource.refreshLogs')">
          <t-button
            size="small"
            variant="text"
            shape="square"
            :loading="loading"
            @click="fetchLogs"
          >
            <template #icon><t-icon name="refresh" /></template>
          </t-button>
        </t-tooltip>
      </div>
    </template>

    <div v-if="loading" style="text-align:center;padding:60px"><t-loading /></div>

    <div v-else-if="logs.length === 0" class="logs-empty">
      <t-icon name="root-list" size="40px" />
      <p>{{ t('datasource.noLogs') }}</p>
    </div>

    <template v-else>
      <!-- Summary -->
      <div class="logs-summary">
        <div class="summary-stat">
          <span class="stat-num">{{ stats.total }}</span>
          <span class="stat-label">{{ t('datasource.logSummary.total') }}</span>
        </div>
        <div class="summary-stat">
          <span class="stat-num success">{{ stats.success }}</span>
          <span class="stat-label">{{ t('datasource.logSummary.success') }}</span>
        </div>
        <div class="summary-stat">
          <span class="stat-num error">{{ stats.failed }}</span>
          <span class="stat-label">{{ t('datasource.logSummary.failed') }}</span>
        </div>
        <div class="summary-stat">
          <span class="stat-num">{{ stats.totalItems }}</span>
          <span class="stat-label">{{ t('datasource.logSummary.items') }}</span>
        </div>
      </div>

      <!-- Timeline grouped by date -->
      <div class="timeline">
        <div v-for="group in groupedLogs" :key="group.date" class="timeline-group">
          <div class="timeline-date">{{ group.date }}</div>

          <div
            v-for="log in group.logs"
            :key="log.id"
            class="timeline-item"
            @click="toggleExpand(log.id)"
          >
            <!-- Dot -->
            <div class="tl-dot-col">
              <span class="tl-dot" :style="{ background: statusColor(log.status) }">
                <t-icon v-if="log.status === 'running'" name="loading" size="10px" class="tl-spin" />
              </span>
              <span class="tl-line"></span>
            </div>

            <!-- Content -->
            <div class="tl-content">
              <div class="tl-header">
                <span class="tl-status" :style="{ color: statusColor(log.status) }">
                  {{ t(`datasource.logStatus.${log.status}`) }}
                </span>
                <span class="tl-time">{{ formatHourMin(log.started_at) }}</span>
                <span v-if="log.finished_at" class="tl-duration">{{ duration(log) }}</span>
              </div>

              <!-- Pills -->
              <div v-if="hasPills(log)" class="tl-pills">
                <span v-if="log.items_created > 0" class="pill created">+{{ log.items_created }}</span>
                <span v-if="log.items_updated > 0" class="pill updated">~{{ log.items_updated }}</span>
                <span v-if="log.items_deleted > 0" class="pill deleted">-{{ log.items_deleted }}</span>
                <span v-if="log.items_skipped > 0" class="pill skipped">{{ log.items_skipped }} {{ t('datasource.logMetric.skipped') }}</span>
                <span v-if="log.items_failed > 0" class="pill failed">{{ log.items_failed }} {{ t('datasource.logMetric.failed') }}</span>
              </div>

              <!-- Expanded -->
              <div v-if="expandedId === log.id" class="tl-detail" @click.stop>
                <div class="detail-row">
                  <span class="detail-label">{{ t('datasource.logDetail.startTime') }}</span>
                  <span>{{ formatTime(log.started_at) }}</span>
                </div>
                <div class="detail-row">
                  <span class="detail-label">{{ t('datasource.logDetail.endTime') }}</span>
                  <span>{{ formatTime(log.finished_at) }}</span>
                </div>
                <div v-if="log.items_total > 0" class="detail-row">
                  <span class="detail-label">{{ t('datasource.logMetric.total') }}</span>
                  <span>{{ log.items_total }}</span>
                </div>
                <div v-if="log.error_message" class="tl-error">
                  {{ log.error_message }}
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="logs-load-more">
          <t-button
            v-if="hasMore"
            variant="outline"
            block
            :loading="loadingMore"
            @click="loadMore"
          >
            {{ t('common.loadMore') }}
          </t-button>
          <span v-else class="logs-load-more-text">{{ t('common.noMoreData') }}</span>
        </div>
      </div>
    </template>
  </t-drawer>
</template>

<style scoped>
.logs-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 80px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  gap: 12px;
}

/* --- Summary --- */
.logs-summary {
  display: flex;
  gap: 8px;
  padding-bottom: 24px;
  margin-bottom: 12px;
  border-bottom: 1px solid var(--td-border-level-1-color);
}

.summary-stat {
  flex: 1;
  text-align: center;
  padding: 16px 8px;
  border-radius: 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-border-level-1-color);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.02);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.stat-num {
  font-size: 20px;
  font-weight: 700;
  color: var(--td-text-color-primary);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.stat-num.success { color: var(--td-success-color); }
.stat-num.error { color: var(--td-error-color); }

.stat-label {
  font-size: 11px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--td-text-color-placeholder);
}

/* --- Timeline --- */
.timeline {
  display: flex;
  flex-direction: column;
}

.timeline-group {
  margin-bottom: 8px;
}

.timeline-date {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  padding: 12px 0 8px 24px;
  position: sticky;
  top: 0;
  background: var(--td-bg-color-container);
  z-index: 1;
  letter-spacing: 0.5px;
  text-transform: uppercase;
}

.timeline-item {
  display: flex;
  cursor: pointer;
  position: relative;
  margin-bottom: 4px;
}

.logs-load-more {
  padding: 16px 0 8px;
  text-align: center;
}

.logs-load-more-text {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

/* --- Dot column: continuous line --- */
.tl-dot-col {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 24px;
  flex-shrink: 0;
  position: relative;
}

/* Continuous vertical line behind dots */
.tl-dot-col::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: 50%;
  width: 1.5px;
  background: var(--td-border-level-1-color);
  transform: translateX(-50%);
}

.timeline-group:last-child .timeline-item:last-child .tl-dot-col::before {
  bottom: 50%;
}

.tl-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  position: relative;
  z-index: 1;
  margin-top: 18px;
  box-shadow: 0 0 0 4px var(--td-bg-color-container);
}

.tl-dot .tl-spin {
  display: none;
}

.tl-line {
  display: none;
}

/* --- Content --- */
.tl-content {
  flex: 1;
  min-width: 0;
  padding: 12px 14px;
  border-radius: 10px;
  transition: background 0.2s ease;
}

.timeline-item:hover .tl-content {
  background: var(--td-bg-color-secondarycontainer);
}

.tl-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tl-status {
  font-size: 13px;
  font-weight: 500;
}

.tl-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.tl-duration {
  margin-left: auto;
  font-size: 11px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
  background: var(--td-bg-color-component);
  padding: 2px 6px;
  border-radius: 4px;
}

/* --- Pills --- */
.tl-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 8px;
}

.pill {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
  line-height: 18px;
  font-variant-numeric: tabular-nums;
}

.pill.created { background: var(--td-success-color-1); color: var(--td-success-color); }
.pill.updated { background: var(--td-brand-color-light); color: var(--td-brand-color); }
.pill.deleted { background: var(--td-warning-color-1); color: var(--td-warning-color); }
.pill.skipped { background: var(--td-bg-color-component); color: var(--td-text-color-placeholder); }
.pill.failed { background: var(--td-error-color-1); color: var(--td-error-color); }

/* --- Expanded detail --- */
.tl-detail {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed var(--td-border-level-2-color);
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.detail-row {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--td-text-color-primary);
  line-height: 20px;
}

.detail-label {
  color: var(--td-text-color-placeholder);
}

.tl-error {
  margin-top: 8px;
  padding: 8px 12px;
  border-radius: 6px;
  background: var(--td-error-color-1);
  color: var(--td-error-color);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
}

/* --- Drawer header --- */
.logs-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.logs-drawer-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* --- Drawer overrides --- */
.ds-logs-drawer :deep(.t-drawer__header) {
  padding: 20px 24px;
  border-bottom: 1px solid var(--td-border-level-1-color);
}

.ds-logs-drawer :deep(.t-drawer__body) {
  padding: 20px 24px;
}
</style>
