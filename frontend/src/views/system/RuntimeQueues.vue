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

    <div v-else-if="!available" class="rq-state">
      <div class="rq-state-icon"><t-icon name="info-circle" size="24px" /></div>
      <div class="rq-state-copy">
        <strong>{{ t('system.globalSettings.runtime.unavailableTitle') }}</strong>
        <span>{{ t('system.globalSettings.runtime.unavailable') }}</span>
      </div>
    </div>

    <template v-else>
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
          <div class="rq-metric rq-capacity">
            <span class="rq-metric-label">{{ t('system.globalSettings.runtime.summary.concurrency') }}</span>
            <span class="rq-capacity-values">
              <span><strong>{{ parseConcurrency }}</strong><small>{{ poolLabel('parse') }}</small></span>
              <i>/</i>
              <span><strong>{{ wikiConcurrency }}</strong><small>{{ poolLabel('wiki') }}</small></span>
            </span>
          </div>
        </div>
      </section>

      <section class="rq-details">
        <div class="rq-details-header">
          <div>
            <h3>{{ t('system.globalSettings.runtime.detailsTitle') }}</h3>
            <p>{{ t('system.globalSettings.runtime.detailsDescription') }}</p>
          </div>
          <span v-if="updatedAt" class="rq-updated-at">
            <t-icon name="time" />
            {{ t('system.globalSettings.runtime.updatedAt', { value: updatedAt }) }}
          </span>
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
                <span class="rq-queue-meta">
                  {{ queueDescription(row.name) }} · {{ poolLabel(row.pool) }} ·
                  {{ t('system.globalSettings.runtime.weight', { value: row.weight }) }}
                </span>
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
              <span class="rq-number" :class="{ 'rq-number--danger': row.archived > 0 }">{{ row.archived }}</span>
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

      <p class="rq-footnote">{{ t('system.globalSettings.runtime.footnote') }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRuntimeQueues, type QueueStat } from '@/api/system'

const { t, te, locale } = useI18n()

const POLL_INTERVAL_MS = 5000

const queues = ref<QueueStat[]>([])
const available = ref(true)
const parseConcurrency = ref(0)
const wikiConcurrency = ref(0)
const loading = ref(false)
const loadedOnce = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const updatedAt = ref('')

let pollTimer: ReturnType<typeof setInterval> | null = null

const columns = computed(() => [
  { colKey: 'name', title: t('system.globalSettings.runtime.columns.queue'), minWidth: 188 },
  { colKey: 'active', title: t('system.globalSettings.runtime.columns.active'), width: 74, align: 'right' as const },
  { colKey: 'pending', title: t('system.globalSettings.runtime.columns.pending'), width: 84, align: 'right' as const },
  { colKey: 'retry', title: t('system.globalSettings.runtime.columns.retry'), width: 68, align: 'right' as const },
  { colKey: 'archived', title: t('system.globalSettings.runtime.columns.archived'), width: 68, align: 'right' as const },
  { colKey: 'latency_ms', title: t('system.globalSettings.runtime.columns.latency'), width: 104, align: 'right' as const },
  { colKey: 'status', title: t('system.globalSettings.runtime.columns.status'), width: 96 },
])

const totalActive = computed(() => queues.value.reduce((s, q) => s + q.active, 0))
const totalPending = computed(() => queues.value.reduce((s, q) => s + q.pending, 0))
const totalRetry = computed(() => queues.value.reduce((s, q) => s + q.retry, 0))

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

function poolLabel(pool: string): string {
  const path = `system.globalSettings.runtime.pools.${pool}`
  return te(path) ? (t(path) as string) : pool
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
  if (row.archived > 0 || row.retry > 0) {
    return { label: t('system.globalSettings.runtime.status.attention'), tone: 'attention' }
  }
  if (row.active > 0) {
    return { label: t('system.globalSettings.runtime.status.working'), tone: 'working' }
  }
  if (row.pending > 0 || row.scheduled > 0) {
    return { label: t('system.globalSettings.runtime.status.waiting'), tone: 'waiting' }
  }
  return { label: t('system.globalSettings.runtime.status.idle'), tone: 'idle' }
}

async function load(showSpinner: boolean) {
  if (showSpinner) loading.value = true
  try {
    const resp = await getRuntimeQueues()
    available.value = resp.available
    parseConcurrency.value = resp.parse_concurrency
    wikiConcurrency.value = resp.wiki_concurrency
    queues.value = resp.queues || []
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

onMounted(() => {
  load(true)
  startPolling()
})

onUnmounted(() => stopPolling())
</script>

<style lang="less" scoped>
.runtime-queues {
  color: var(--td-text-color-primary);
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
    font-size: 13px;
    line-height: 1.65;
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
  font-size: 12px;
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
    font-size: 13px;
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
  font-size: 13px;
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
  grid-template-columns: repeat(3, minmax(64px, 1fr)) minmax(190px, 1.8fr);
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
  font-size: 11px;
  line-height: 1.35;
  white-space: nowrap;
}

.rq-metric--warning .rq-metric-value {
  color: var(--td-warning-color);
}

.rq-capacity {
  padding-left: 28px;
  border-left: 1px solid var(--td-component-stroke);
}

.rq-capacity-values {
  display: flex;
  min-height: 22px;
  align-items: center;
  gap: 10px;
  font-variant-numeric: tabular-nums;

  > span {
    display: flex;
    align-items: baseline;
    gap: 6px;
  }

  i {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-style: normal;
  }

  small {
    color: var(--td-text-color-secondary);
    font-size: 10px;
    white-space: nowrap;
  }

  strong {
    color: var(--td-text-color-primary);
    font-size: 20px;
    font-weight: 600;
    line-height: 1.1;
    letter-spacing: -0.02em;
  }
}

.rq-details {
  margin-top: 2px;
}

.rq-details-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 13px;

  h3 {
    margin: 0 0 4px;
    color: var(--td-text-color-primary);
    font-size: 15px;
    font-weight: 600;
  }

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.5;
  }
}

.rq-updated-at {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
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
  font-size: 13px;
  font-weight: 500;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rq-queue-meta {
  color: var(--td-text-color-placeholder);
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 10px;
  line-height: 1.35;
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

.rq-backlog {
  display: flex;
  align-items: flex-end;
  flex-direction: column;
  gap: 1px;

  small {
    color: var(--td-text-color-placeholder);
    font-size: 9px;
    white-space: nowrap;
  }
}

.rq-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: 11px;
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
}

.data-table-shell.rq-table-shell {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    height: 38px;
    color: var(--td-text-color-secondary);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.01em;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td) {
    height: 54px;
    padding-top: 8px;
    padding-bottom: 8px;
    font-variant-numeric: tabular-nums;
  }

  &:deep(.t-table__body tr:last-child td) {
    border-bottom: 0;
  }
}

.rq-footnote {
  margin: 12px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  line-height: 1.5;
}

@media (max-width: 860px) {
  .rq-header,
  .rq-details-header {
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
    grid-template-columns: repeat(3, minmax(64px, 1fr)) minmax(180px, 1.6fr);
  }

  .rq-capacity {
    padding-left: 20px;
  }
}

@media (max-width: 620px) {
  .rq-loading-metrics {
    grid-template-columns: 1fr;
  }

  .rq-overview-metrics {
    width: 100%;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
  }

  .rq-capacity {
    grid-column: 1 / -1;
    padding-top: 12px;
    padding-left: 0;
    border-top: 1px solid var(--td-component-stroke);
    border-left: 0;
  }

  .rq-state {
    grid-template-columns: auto minmax(0, 1fr);

    .t-button {
      grid-column: 2;
      justify-self: start;
    }
  }
}
</style>
