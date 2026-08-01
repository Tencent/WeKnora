<template>
  <div class="model-usage-settings">
    <div class="section-header">
      <div>
        <h2>{{ t('modelUsage.title') }}</h2>
        <p class="section-description">{{ t('modelUsage.description') }}</p>
      </div>
      <div class="header-actions">
        <span class="refresh-state">
          <span class="refresh-dot" :class="{ 'refresh-dot--on': autoRefresh }" />
          {{ autoRefresh ? t('modelUsage.autoRefreshOn') : t('modelUsage.autoRefreshOff') }}
        </span>
        <t-button variant="outline" size="small" :loading="refreshing" @click="loadUsage(false)">
          <template #icon><t-icon name="refresh" /></template>
          {{ t('common.refresh') }}
        </t-button>
      </div>
    </div>

    <div class="usage-toolbar">
      <t-radio-group v-model="range" variant="default-filled" size="small">
        <t-radio-button v-for="item in rangeOptions" :key="item.value" :value="item.value">
          {{ item.label }}
        </t-radio-button>
      </t-radio-group>

      <t-select v-model="modelType" size="small" class="type-filter" :options="typeOptions" />

      <div class="auto-refresh-control">
        <span>{{ t('modelUsage.autoRefresh') }}</span>
        <t-switch v-model="autoRefresh" size="small" />
      </div>
    </div>

    <t-alert v-if="error" theme="error" :message="error" class="usage-alert">
      <template #operation>
        <t-button size="small" variant="text" @click="loadUsage(true)">
          {{ t('common.retry') }}
        </t-button>
      </template>
    </t-alert>

    <t-loading :loading="loading" size="small" class="usage-loading">
      <div class="metric-grid">
        <div class="metric-card">
          <span class="metric-label">{{ t('modelUsage.metrics.totalTokens') }}</span>
          <strong>{{ formatNumber(summary.total_tokens) }}</strong>
          <span>{{ t('modelUsage.metrics.cached', { count: formatNumber(summary.cached_tokens) }) }}</span>
        </div>
        <div class="metric-card">
          <span class="metric-label">{{ t('modelUsage.metrics.calls') }}</span>
          <strong>{{ formatNumber(summary.total_calls) }}</strong>
          <span>{{ t('modelUsage.metrics.errors', { count: formatNumber(summary.error_count) }) }}</span>
        </div>
        <div class="metric-card">
          <span class="metric-label">{{ t('modelUsage.metrics.promptCompletion') }}</span>
          <strong>{{ formatNumber(summary.prompt_tokens) }} / {{ formatNumber(summary.completion_tokens) }}</strong>
          <span>{{ t('modelUsage.metrics.promptCompletionHint') }}</span>
        </div>
        <div class="metric-card">
          <span class="metric-label">{{ t('modelUsage.metrics.successRate') }}</span>
          <strong>{{ formatPercent(summary.success_rate) }}</strong>
          <span>{{ windowLabel }}</span>
        </div>
      </div>

      <div v-if="!loading && !report.models.length" class="empty-state">
        <t-empty :description="t('modelUsage.empty')" />
      </div>

      <template v-else>
        <section class="usage-panel chart-panel">
          <div class="panel-header">
            <h3>{{ t('modelUsage.timeline.title') }}</h3>
            <span>{{ t('modelUsage.timeline.subtitle') }}</span>
          </div>
          <div v-if="chartBuckets.length" class="usage-chart">
            <div class="chart-plot">
              <div v-for="bucket in chartBuckets" :key="bucket.bucket_start" class="chart-column">
                <span class="chart-value">{{ formatCompactNumber(bucket.total_tokens) }}</span>
                <div
                  class="chart-bar"
                  :style="{ height: `${barHeight(bucket.total_tokens)}%` }"
                  :title="bucketTooltip(bucket)"
                >
                  <div
                    v-for="segment in bucket.segments"
                    :key="segment.model_key"
                    class="chart-segment"
                    :style="{ height: `${segment.percent}%`, backgroundColor: segment.color }"
                    :title="segmentTooltip(bucket, segment)"
                  />
                </div>
                <span class="chart-time">{{ formatBucketLabel(bucket.bucket_start) }}</span>
              </div>
            </div>
            <div class="chart-legend">
              <div v-for="item in modelLegend" :key="item.model_key" class="legend-item">
                <span class="legend-swatch" :style="{ backgroundColor: item.color }" />
                <span class="legend-name">{{ item.model_name }}</span>
                <span class="legend-value">{{ formatNumber(item.total_tokens) }}</span>
              </div>
            </div>
          </div>
          <div v-else class="chart-empty">
            <t-empty :description="t('modelUsage.empty')" />
          </div>
        </section>

        <section class="usage-panel">
          <div class="panel-header">
            <h3>{{ t('modelUsage.table.title') }}</h3>
            <span>{{ t('modelUsage.table.subtitle') }}</span>
          </div>
          <t-table
            row-key="row_key"
            :data="modelRows"
            :columns="columns"
            size="medium"
            hover
            stripe
          >
            <template #model="{ row }">
              <div class="model-cell">
                <span class="model-cell__name">{{ row.display_name || row.model_name || '-' }}</span>
                <span v-if="row.display_name && row.model_name" class="model-cell__id">{{ row.model_name }}</span>
              </div>
            </template>
            <template #model_type="{ row }">
              <t-tag size="small" variant="light-outline">{{ modelTypeLabel(row.model_type) }}</t-tag>
            </template>
            <template #provider="{ row }">
              <span class="provider-cell">{{ providerLabel(row) }}</span>
            </template>
            <template #total_tokens="{ row }">
              <span class="numeric strong">{{ formatNumber(row.total_tokens) }}</span>
            </template>
            <template #token_breakdown="{ row }">
              <div class="token-breakdown">
                <span>{{ t('modelUsage.columns.promptShort') }} {{ formatNumber(row.prompt_tokens) }}</span>
                <span>{{ t('modelUsage.columns.completionShort') }} {{ formatNumber(row.completion_tokens) }}</span>
                <span>{{ t('modelUsage.columns.cachedShort') }} {{ formatNumber(row.cached_tokens) }}</span>
              </div>
            </template>
            <template #success_rate="{ row }">
              <span :class="['rate-cell', { 'rate-cell--warn': row.error_count > 0 }]">
                {{ formatPercent(row.success_rate) }}
              </span>
            </template>
            <template #last_used_at="{ row }">
              <span>{{ formatDate(row.last_used_at) }}</span>
            </template>
          </t-table>
        </section>

        <section class="usage-panel recent-panel">
          <div class="panel-header">
            <h3>{{ t('modelUsage.recent.title') }}</h3>
            <span>{{ t('modelUsage.recent.subtitle') }}</span>
          </div>
          <div class="recent-list">
            <div v-for="event in report.recent_events" :key="event.id" class="recent-row">
              <div class="recent-main">
                <span class="recent-model">{{ event.model_name || '-' }}</span>
                <t-tag size="small" :theme="event.success ? 'success' : 'danger'" variant="light">
                  {{ event.success ? t('modelUsage.recent.success') : t('modelUsage.recent.failed') }}
                </t-tag>
                <t-tag size="small" variant="light-outline">{{ event.request_kind }}</t-tag>
                <t-tag size="small" variant="light-outline">{{ usageSourceLabel(event.usage_source) }}</t-tag>
              </div>
              <div class="recent-meta">
                <span>{{ formatNumber(event.total_tokens) }} tokens</span>
                <span>{{ formatDuration(event.duration_ms) }}</span>
                <span>{{ formatDate(event.created_at) }}</span>
              </div>
            </div>
          </div>
        </section>
      </template>
    </t-loading>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getModelUsage,
  type ModelUsageModelStats,
  type ModelUsageRange,
  type ModelUsageReport,
  type ModelUsageType,
} from '@/api/model'

type ModelUsageRow = ModelUsageModelStats & { row_key: string }
type ChartSegment = {
  model_key: string
  model_name: string
  total_tokens: number
  calls: number
  error_count: number
  percent: number
  color: string
}
type ChartBucket = {
  bucket_start: string
  total_tokens: number
  calls: number
  error_count: number
  segments: ChartSegment[]
}
type ChartLegendItem = {
  model_key: string
  model_name: string
  total_tokens: number
  color: string
}

const { t, locale } = useI18n()

const range = ref<ModelUsageRange>('24h')
const modelType = ref<ModelUsageType>('all')
const autoRefresh = ref(true)
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')
const report = ref<ModelUsageReport>(emptyReport())
let timer: ReturnType<typeof setInterval> | undefined
let requestSeq = 0

const rangeOptions = computed(() => [
  { value: '15m', label: t('modelUsage.ranges.15m') },
  { value: '1h', label: t('modelUsage.ranges.1h') },
  { value: '24h', label: t('modelUsage.ranges.24h') },
  { value: '7d', label: t('modelUsage.ranges.7d') },
])

const typeOptions = computed(() => [
  { value: 'all', label: t('common.all') },
  { value: 'KnowledgeQA', label: t('modelUsage.types.KnowledgeQA') },
  { value: 'Embedding', label: t('modelUsage.types.Embedding') },
  { value: 'Rerank', label: t('modelUsage.types.Rerank') },
  { value: 'VLLM', label: t('modelUsage.types.VLLM') },
  { value: 'ASR', label: t('modelUsage.types.ASR') },
])

const columns = computed(() => [
  { colKey: 'model', title: t('modelUsage.columns.model'), minWidth: 220, ellipsis: true },
  { colKey: 'model_type', title: t('modelUsage.columns.type'), width: 104 },
  { colKey: 'provider', title: t('modelUsage.columns.provider'), width: 132, ellipsis: true },
  { colKey: 'calls', title: t('modelUsage.columns.calls'), width: 96, align: 'right' },
  { colKey: 'total_tokens', title: t('modelUsage.columns.totalTokens'), width: 132, align: 'right' },
  { colKey: 'token_breakdown', title: t('modelUsage.columns.breakdown'), minWidth: 210 },
  { colKey: 'error_count', title: t('modelUsage.columns.errors'), width: 88, align: 'right' },
  { colKey: 'success_rate', title: t('modelUsage.columns.successRate'), width: 112, align: 'right' },
  { colKey: 'last_used_at', title: t('modelUsage.columns.lastUsed'), width: 168 },
])

const summary = computed(() => report.value.summary)
const modelRows = computed<ModelUsageRow[]>(() =>
  report.value.models.map((row, index) => ({
    ...row,
    row_key: `${row.model_id || row.model_name || 'model'}-${index}`,
  })),
)

const chartPalette = [
  '#2f7cf6',
  '#00a870',
  '#f5a623',
  '#e34d59',
  '#7c3aed',
  '#14b8a6',
  '#f97316',
  '#64748b',
  '#d946ef',
  '#0ea5e9',
  '#84cc16',
  '#ef4444',
]
const minChartBucketCalls = 100

const modelNameMap = computed(() => {
  const names = new Map<string, string>()
  for (const row of report.value.models) {
    names.set(modelUsageKey(row.model_id, row.model_name, row.model_type), row.display_name || row.model_name || '-')
  }
  return names
})

const modelColorMap = computed(() => {
  const colors = new Map<string, string>()
  const keys = new Set<string>()
  for (const row of report.value.models) {
    keys.add(modelUsageKey(row.model_id, row.model_name, row.model_type))
  }
  for (const point of report.value.timeline) {
    keys.add(modelUsageKey(point.model_id, point.model_name, point.model_type))
  }
  Array.from(keys).forEach((key, index) => {
    colors.set(key, chartPalette[index % chartPalette.length])
  })
  return colors
})

const chartBuckets = computed<ChartBucket[]>(() => {
  const buckets = new Map<
    string,
    {
      bucket_start: string
      total_tokens: number
      calls: number
      error_count: number
      models: Map<string, Omit<ChartSegment, 'percent' | 'color'>>
    }
  >()
  for (const point of report.value.timeline) {
    const existing = buckets.get(point.bucket_start) || {
      bucket_start: point.bucket_start,
      total_tokens: 0,
      calls: 0,
      error_count: 0,
      models: new Map<string, Omit<ChartSegment, 'percent' | 'color'>>(),
    }
    existing.total_tokens += point.total_tokens
    existing.calls += point.calls
    existing.error_count += point.error_count
    const key = modelUsageKey(point.model_id, point.model_name, point.model_type)
    const model = existing.models.get(key) || {
      model_key: key,
      model_name: modelNameMap.value.get(key) || point.model_name || '-',
      total_tokens: 0,
      calls: 0,
      error_count: 0,
    }
    model.total_tokens += point.total_tokens
    model.calls += point.calls
    model.error_count += point.error_count
    existing.models.set(key, model)
    buckets.set(point.bucket_start, existing)
  }
  return Array.from(buckets.values())
    .sort((a, b) => new Date(a.bucket_start).getTime() - new Date(b.bucket_start).getTime())
    .filter((bucket) => bucket.calls >= minChartBucketCalls)
    .slice(-24)
    .map((bucket) => ({
      bucket_start: bucket.bucket_start,
      total_tokens: bucket.total_tokens,
      calls: bucket.calls,
      error_count: bucket.error_count,
      segments: Array.from(bucket.models.values())
        .sort((a, b) => b.total_tokens - a.total_tokens)
        .map((segment) => ({
          ...segment,
          percent: bucket.total_tokens > 0 ? (segment.total_tokens / bucket.total_tokens) * 100 : 0,
          color: modelColorMap.value.get(segment.model_key) || chartPalette[0],
        })),
    }))
})

const modelLegend = computed<ChartLegendItem[]>(() => {
  const totals = new Map<string, ChartLegendItem>()
  for (const bucket of chartBuckets.value) {
    for (const segment of bucket.segments) {
      const existing = totals.get(segment.model_key) || {
        model_key: segment.model_key,
        model_name: segment.model_name,
        total_tokens: 0,
        color: segment.color,
      }
      existing.total_tokens += segment.total_tokens
      totals.set(segment.model_key, existing)
    }
  }
  return Array.from(totals.values()).sort((a, b) => b.total_tokens - a.total_tokens)
})

const maxChartTokens = computed(() => Math.max(1, ...chartBuckets.value.map((row) => row.total_tokens)))
const windowLabel = computed(() => `${formatDate(summary.value.window_start)} - ${formatDate(summary.value.window_end)}`)

function emptyReport(): ModelUsageReport {
  const now = new Date().toISOString()
  return {
    summary: {
      window_start: now,
      window_end: now,
      refresh_seconds: 5,
      total_calls: 0,
      total_tokens: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
      error_count: 0,
      success_rate: 1,
    },
    models: [],
    timeline: [],
    recent_events: [],
  }
}

async function loadUsage(showLoading = true) {
  const seq = ++requestSeq
  if (showLoading) loading.value = true
  refreshing.value = !showLoading
  error.value = ''
  try {
    const data = await getModelUsage({
      range: range.value,
      model_type: modelType.value,
    })
    if (seq === requestSeq) {
      report.value = data
    }
  } catch (err: any) {
    if (seq === requestSeq) {
      error.value = err?.message || t('modelUsage.loadFailed')
    }
  } finally {
    if (seq === requestSeq) {
      loading.value = false
      refreshing.value = false
    }
  }
}

function resetTimer() {
  if (timer) {
    clearInterval(timer)
    timer = undefined
  }
  if (autoRefresh.value) {
    timer = setInterval(() => loadUsage(false), 5000)
  }
}

function formatNumber(value?: number | null) {
  return new Intl.NumberFormat(locale.value || 'zh-CN').format(value || 0)
}

function formatCompactNumber(value?: number | null) {
  return new Intl.NumberFormat(locale.value || 'zh-CN', {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value || 0)
}

function formatPercent(value?: number | null) {
  return `${(((value ?? 0) || 0) * 100).toFixed(1)}%`
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function formatBucketLabel(value: string) {
  if (range.value === '7d') return formatDate(value)
  return formatTime(value)
}

function formatDuration(ms?: number | null) {
  const value = ms || 0
  if (value < 1000) return `${value}ms`
  return `${(value / 1000).toFixed(1)}s`
}

function barHeight(totalTokens: number) {
  if (totalTokens <= 0) return 0
  return Math.max(6, Math.round((totalTokens / maxChartTokens.value) * 100))
}

function modelUsageKey(modelID?: string, modelName?: string, type?: string) {
  return [modelID || modelName || 'unknown', type || 'unknown'].join(':')
}

function bucketTooltip(bucket: ChartBucket) {
  const lines = [
    `${formatDate(bucket.bucket_start)} · ${formatNumber(bucket.total_tokens)} tokens · ${formatNumber(bucket.calls)} calls`,
    ...bucket.segments.map(
      (segment) =>
        `${segment.model_name}: ${formatNumber(segment.total_tokens)} tokens, ${formatNumber(segment.calls)} calls`,
    ),
  ]
  return lines.join('\n')
}

function segmentTooltip(bucket: ChartBucket, segment: ChartSegment) {
  const share = bucket.total_tokens > 0 ? segment.total_tokens / bucket.total_tokens : 0
  return `${segment.model_name}\n${formatNumber(segment.total_tokens)} tokens · ${formatNumber(segment.calls)} calls · ${formatPercent(share)}`
}

function modelTypeLabel(type: string) {
  const key = `modelUsage.types.${type}`
  return t(key)
}

function providerLabel(row: ModelUsageModelStats) {
  if (row.provider) return row.provider
  return row.model_source || '-'
}

function usageSourceLabel(source: string) {
  const key = `modelUsage.usageSource.${source || 'missing'}`
  return t(key)
}

watch([range, modelType], () => {
  loadUsage(true)
})

watch(autoRefresh, resetTimer)

onMounted(() => {
  loadUsage(true)
  resetTimer()
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped lang="less">
.model-usage-settings {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-start;
}

.section-header h2 {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-description {
  margin: 8px 0 0;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
}

.header-actions,
.usage-toolbar,
.auto-refresh-control {
  display: flex;
  align-items: center;
  gap: 12px;
}

.header-actions {
  flex-shrink: 0;
}

.refresh-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.refresh-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--td-gray-color-5);
}

.refresh-dot--on {
  background: var(--td-success-color);
}

.usage-toolbar {
  flex-wrap: wrap;
  padding: 12px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
  background: var(--td-bg-color-container);
}

.type-filter {
  width: 168px;
}

.auto-refresh-control {
  margin-left: auto;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.usage-alert {
  margin-bottom: -4px;
}

.usage-loading {
  min-height: 240px;
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.metric-card {
  min-height: 104px;
  padding: 16px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.metric-card strong {
  display: block;
  margin: 8px 0 6px;
  font-size: 24px;
  line-height: 1.2;
  color: var(--td-text-color-primary);
}

.metric-card span {
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.metric-label {
  color: var(--td-text-color-placeholder) !important;
}

.usage-panel {
  margin-top: 14px;
  padding: 16px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
  background: var(--td-bg-color-container);
}

.panel-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: baseline;
  margin-bottom: 14px;
}

.panel-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.panel-header span {
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.chart-panel {
  overflow: hidden;
}

.usage-chart {
  display: grid;
  gap: 14px;
}

.chart-plot {
  min-height: 260px;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(34px, 1fr));
  align-items: end;
  gap: 10px;
  padding: 12px 4px 4px;
  border-bottom: 1px solid var(--td-border-level-1-color);
  overflow-x: auto;
}

.chart-column {
  min-width: 34px;
  height: 232px;
  display: grid;
  grid-template-rows: 22px 1fr 32px;
  align-items: end;
  justify-items: center;
  gap: 6px;
}

.chart-value {
  max-width: 58px;
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chart-bar {
  width: 100%;
  max-width: 34px;
  min-height: 8px;
  display: flex;
  flex-direction: column-reverse;
  overflow: hidden;
  border-radius: 6px 6px 2px 2px;
  background: var(--td-bg-color-component);
  box-shadow: inset 0 0 0 1px var(--td-border-level-1-color);
}

.chart-segment {
  width: 100%;
  min-height: 2px;
  transition: opacity 0.15s ease;
}

.chart-segment:hover {
  opacity: 0.78;
}

.chart-time {
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 1.2;
  text-align: center;
  white-space: normal;
  word-break: keep-all;
}

.chart-legend {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px 14px;
}

.legend-item {
  min-width: 0;
  display: grid;
  grid-template-columns: 10px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.legend-swatch {
  width: 10px;
  height: 10px;
  border-radius: 2px;
}

.legend-name {
  overflow: hidden;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.legend-value {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.chart-empty {
  padding: 24px 0;
}

.model-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.model-cell__name {
  color: var(--td-text-color-primary);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-cell__id {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-cell,
.numeric,
.rate-cell {
  font-variant-numeric: tabular-nums;
}

.strong {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.token-breakdown {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.rate-cell--warn {
  color: var(--td-warning-color);
}

.recent-list {
  display: grid;
  gap: 10px;
}

.recent-row {
  display: flex;
  justify-content: space-between;
  gap: 14px;
  padding: 10px 0;
  border-bottom: 1px solid var(--td-border-level-1-color);
}

.recent-row:last-child {
  border-bottom: 0;
}

.recent-main,
.recent-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.recent-model {
  color: var(--td-text-color-primary);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.recent-meta {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.empty-state {
  padding: 48px 0;
}

@media (max-width: 960px) {
  .section-header,
  .recent-row {
    flex-direction: column;
  }

  .header-actions,
  .auto-refresh-control {
    margin-left: 0;
  }

  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .recent-meta {
    flex-wrap: wrap;
  }
}

@media (max-width: 640px) {
  .metric-grid {
    grid-template-columns: 1fr;
  }

  .chart-plot {
    grid-template-columns: repeat(24, minmax(28px, 1fr));
    gap: 8px;
  }

  .chart-column {
    min-width: 28px;
  }

  .chart-legend {
    grid-template-columns: 1fr;
  }
}
</style>
