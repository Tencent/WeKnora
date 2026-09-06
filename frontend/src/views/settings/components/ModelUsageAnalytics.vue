<template>
  <section class="usage-analytics" :aria-busy="loading">
    <div class="analytics-filters">
      <div class="analytics-filter analytics-filter--model">
        <label>{{ t('modelSettings.analytics.model') }}</label>
        <t-select
          v-model="selectedModelId"
          :options="modelOptions"
          :loading="modelsLoading"
          filterable
        />
      </div>
      <div class="analytics-filter analytics-filter--date">
        <label>{{ t('modelSettings.analytics.dateRange') }}</label>
        <t-date-range-picker
          v-model="dateRange"
          :placeholder="[
            t('modelSettings.analytics.startDate'),
            t('modelSettings.analytics.endDate'),
          ]"
          :disable-date="disableFutureDate"
          allow-input
        >
          <template #prefixIcon><t-icon name="calendar" /></template>
        </t-date-range-picker>
      </div>
      <div class="analytics-filter analytics-filter--interval">
        <label>{{ t('modelSettings.analytics.interval') }}</label>
        <t-radio-group v-model="interval" class="analytics-segmented" variant="default-filled">
          <t-radio-button value="day">{{ t('modelSettings.analytics.day') }}</t-radio-button>
          <t-radio-button value="hour">{{ t('modelSettings.analytics.hour') }}</t-radio-button>
        </t-radio-group>
      </div>
    </div>

    <p v-if="modelsLoadFailed" class="analytics-filter-warning" role="status">
      <t-icon name="info-circle" />
      {{ t('modelSettings.analytics.modelsLoadFailed') }}
    </p>

    <t-loading :loading="loading" size="small" class="analytics-content">
      <div v-if="loadFailed" class="analytics-state analytics-state--error" role="alert">
        <t-icon name="error-circle" size="36px" />
        <p>{{ t('modelSettings.analytics.loadFailed') }}</p>
        <t-button theme="primary" variant="outline" @click="loadAnalytics">
          {{ t('modelSettings.analytics.retry') }}
        </t-button>
      </div>

      <div v-else-if="isEmpty" class="analytics-state">
        <t-empty :description="t('modelSettings.analytics.empty')" />
      </div>

      <template v-else-if="analytics">
        <div class="analytics-summary-grid">
          <article class="analytics-summary-card">
            <div class="summary-card__icon"><t-icon name="chart-line" /></div>
            <p class="summary-card__label">{{ t('modelSettings.analytics.calls') }}</p>
            <p class="summary-card__value" :title="exactTitle(analytics.summary.calls.total)">
              {{ formatExactNumber(analytics.summary.calls.total, locale) }}
            </p>
            <p class="summary-card__meta summary-card__meta--breakdown" :title="callBreakdown">
              {{ callBreakdown }}
            </p>
          </article>

          <article class="analytics-summary-card">
            <div class="summary-card__icon"><t-icon name="data" /></div>
            <p class="summary-card__label">{{ t('modelSettings.analytics.totalTokens') }}</p>
            <p class="summary-card__value" :title="exactTitle(analytics.summary.total_tokens.sum)">
              {{ formatCompactNumber(analytics.summary.total_tokens.sum, locale) }}
            </p>
            <div class="summary-card__meta summary-card__meta--tokens">
              <span>{{ t('modelSettings.analytics.inputDetail', {
                value: formatCompactNumber(analytics.summary.input_tokens.sum, locale),
              }) }}</span>
              <span>{{ t('modelSettings.analytics.outputDetail', {
                value: formatCompactNumber(analytics.summary.output_tokens.sum, locale),
              }) }}</span>
            </div>
          </article>

          <article class="analytics-summary-card">
            <div class="summary-card__icon"><t-icon name="arrow-down" /></div>
            <p class="summary-card__label">{{ t('modelSettings.analytics.inputTokens') }}</p>
            <p class="summary-card__value" :title="exactTitle(analytics.summary.input_tokens.sum)">
              {{ formatCompactNumber(analytics.summary.input_tokens.sum, locale) }}
            </p>
            <p class="summary-card__meta">
              {{ coverageLabel(
                analytics.summary.input_tokens.observed_calls,
                analytics.summary.input_tokens.applicable_calls,
              ) }}
            </p>
          </article>

          <article class="analytics-summary-card">
            <div class="summary-card__icon"><t-icon name="time" /></div>
            <p class="summary-card__label">{{ t('modelSettings.analytics.avgLatency') }}</p>
            <p class="summary-card__value" :title="exactLatencyTitle(analytics.summary.latency.avg_ms)">
              {{ formatLatency(analytics.summary.latency.avg_ms, locale) }}
            </p>
            <p class="summary-card__meta">
              <template v-if="analytics.summary.latency.observed_calls < analytics.summary.latency.applicable_calls">
                {{ coverageLabel(
                  analytics.summary.latency.observed_calls,
                  analytics.summary.latency.applicable_calls,
                ) }}
              </template>
            </p>
          </article>
        </div>

        <section class="analytics-panel analytics-trend-panel">
          <div class="analytics-panel__header analytics-trend-header">
            <div>
              <h3>{{ t('modelSettings.analytics.usageTrend') }} <span>· UTC</span></h3>
            </div>
            <t-radio-group v-model="trendMetric" class="analytics-segmented" variant="default-filled" size="small">
              <t-radio-button value="calls">{{ t('modelSettings.analytics.calls') }}</t-radio-button>
              <t-radio-button value="tokens">{{ t('modelSettings.analytics.tokens') }}</t-radio-button>
              <t-radio-button value="latency">{{ t('modelSettings.analytics.latency') }}</t-radio-button>
            </t-radio-group>
          </div>

          <div v-if="!chartHasData" class="analytics-chart-empty">
            <t-icon name="chart-line" size="32px" />
            <span>{{ t('modelSettings.analytics.noMetricData') }}</span>
          </div>
          <div v-else class="analytics-chart-wrap">
            <svg
              class="analytics-chart"
              :viewBox="`0 0 ${chartWidth} ${chartHeight}`"
              role="img"
              :aria-label="chartAriaLabel"
            >
              <g class="chart-grid">
                <template v-for="tick in chartYTicks" :key="tick.y">
                  <line :x1="plotLeft" :x2="chartWidth - plotRight" :y1="tick.y" :y2="tick.y" />
                  <text :x="plotLeft - 10" :y="tick.y + 4" text-anchor="end">{{ tick.label }}</text>
                </template>
              </g>
              <line
                class="chart-axis"
                :x1="plotLeft"
                :x2="chartWidth - plotRight"
                :y1="plotBottomY"
                :y2="plotBottomY"
              />
              <path
                v-for="(path, index) in chartPaths"
                :key="index"
                class="chart-line"
                :d="path"
              />
              <g
                v-for="point in visibleChartPoints"
                :key="point.bucketStart"
                class="chart-point-target"
                tabindex="0"
                :aria-label="point.title"
              >
                <title>{{ point.title }}</title>
                <circle class="chart-point-hit-area" :cx="point.x" :cy="point.y" r="10" />
                <circle class="chart-point" :cx="point.x" :cy="point.y" r="3" />
              </g>
              <g class="chart-x-labels">
                <text
                  v-for="tick in chartXTicks"
                  :key="tick.bucketStart"
                  :x="tick.x"
                  :y="chartHeight - 8"
                  :text-anchor="tick.anchor"
                >{{ tick.label }}</text>
              </g>
            </svg>
          </div>
        </section>

        <div class="analytics-cache-grid">
          <section class="analytics-panel cache-panel">
            <div class="analytics-panel__header">
              <div>
                <h3>{{ t('modelSettings.analytics.promptCache') }}</h3>
                <p>{{ t('modelSettings.analytics.providerCache') }}</p>
              </div>
              <t-icon name="layers" size="22px" />
            </div>
            <div class="cache-metrics">
              <div>
                <span>{{ t('modelSettings.analytics.callHitRate') }}</span>
                <strong>{{ formatRatio(analytics.summary.prompt_cache.call_hit_rate, locale) }}</strong>
              </div>
              <div>
                <span>{{ t('modelSettings.analytics.tokenCacheRatio') }}</span>
                <strong>{{ formatRatio(analytics.summary.prompt_cache.token_cache_ratio, locale) }}</strong>
              </div>
            </div>
            <p v-if="analytics.summary.prompt_cache.eligible_calls > 0" class="cache-panel__meta">
              {{ t('modelSettings.analytics.promptCacheDetail', {
                hit: formatExactNumber(analytics.summary.prompt_cache.hit_calls, locale),
                miss: formatExactNumber(analytics.summary.prompt_cache.miss_calls, locale),
                eligible: formatExactNumber(analytics.summary.prompt_cache.eligible_calls, locale),
              }) }}
            </p>
          </section>

          <section class="analytics-panel cache-panel">
            <div class="analytics-panel__header">
              <div>
                <h3>{{ t('modelSettings.analytics.embeddingCache') }}</h3>
                <p>{{ t('modelSettings.analytics.weknoraCache') }}</p>
              </div>
              <t-icon name="chart-bubble" size="22px" />
            </div>
            <div class="cache-metrics cache-metrics--single">
              <div>
                <span>{{ t('modelSettings.analytics.inputHitRate') }}</span>
                <strong>{{ formatRatio(analytics.summary.embedding_cache.input_hit_rate, locale) }}</strong>
              </div>
            </div>
            <p v-if="analytics.summary.embedding_cache.eligible_inputs > 0" class="cache-panel__meta">
              {{ t('modelSettings.analytics.embeddingCacheDetail', {
                hit: formatExactNumber(analytics.summary.embedding_cache.cache_hits, locale),
                miss: formatExactNumber(analytics.summary.embedding_cache.cache_misses, locale),
                eligible: formatExactNumber(analytics.summary.embedding_cache.eligible_inputs, locale),
              }) }}
            </p>
          </section>
        </div>
      </template>

      <div v-else class="analytics-loading-placeholder" aria-hidden="true" />
    </t-loading>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { listModels, type ModelConfig } from '@/api/model'
import {
  getModelUsageAnalytics,
  type ModelUsageAnalyticsBucket,
  type ModelUsageAnalyticsInterval,
  type ModelUsageAnalyticsResult,
} from '@/api/modelUsageAnalytics'
import {
  defaultAnalyticsDateRange,
  formatCompactNumber,
  formatExactNumber,
  formatLatency,
  formatRatio,
  inclusiveDateRangeToExclusiveRFC3339,
} from './modelUsageAnalyticsHelpers'

type TrendMetric = 'calls' | 'tokens' | 'latency'

interface ChartPoint {
  bucketStart: string
  timestamp: number
  value: number | null
  x: number
  y: number | null
  title: string
}

const { t, locale } = useI18n()
const analytics = ref<ModelUsageAnalyticsResult | null>(null)
const loading = ref(false)
const loadFailed = ref(false)
const modelsLoading = ref(false)
const modelsLoadFailed = ref(false)
const models = ref<ModelConfig[]>([])
const selectedModelId = ref('')
const dateRange = ref<string[]>(defaultAnalyticsDateRange())
const interval = ref<ModelUsageAnalyticsInterval>('day')
const trendMetric = ref<TrendMetric>('calls')
let requestSequence = 0

const disableFutureDate = { after: new Date(new Date().setHours(23, 59, 59, 999)) }

const modelOptions = computed(() => [
  { label: t('modelSettings.analytics.allModels'), value: '' },
  ...models.value
    .filter((model): model is ModelConfig & { id: string } => typeof model.id === 'string' && model.id !== '')
    .map(model => ({
      label: model.display_name?.trim() || model.name,
      value: model.id,
    })),
])

const isEmpty = computed(() => (
  analytics.value !== null
  && analytics.value.summary.calls.total === 0
  && analytics.value.trend.length === 0
))

const callBreakdown = computed(() => {
  const calls = analytics.value?.summary.calls
  if (!calls) return ''
  return (['chat', 'embedding', 'rerank'] as const)
    .filter(type => calls[type] > 0)
    .map(type => `${t(`modelSettings.typeShort.${type}`)} ${formatExactNumber(calls[type], locale.value)}`)
    .join(' · ')
})

async function loadModelsForFilter() {
  modelsLoading.value = true
  modelsLoadFailed.value = false
  try {
    models.value = await listModels()
  } catch (error) {
    console.error('Failed to load analytics model filter:', error)
    modelsLoadFailed.value = true
  } finally {
    modelsLoading.value = false
  }
}

async function loadAnalytics() {
  const sequence = ++requestSequence
  let bounds: { startTime: string; endTime: string }
  try {
    bounds = inclusiveDateRangeToExclusiveRFC3339(dateRange.value)
  } catch {
    if (sequence === requestSequence) loading.value = false
    return
  }
  loading.value = true
  loadFailed.value = false
  try {
    const result = await getModelUsageAnalytics({
      modelId: selectedModelId.value || undefined,
      startTime: bounds.startTime,
      endTime: bounds.endTime,
      interval: interval.value,
    })
    if (sequence === requestSequence) analytics.value = result
  } catch (error) {
    if (sequence !== requestSequence) return
    console.error('Failed to load model usage analytics:', error)
    analytics.value = null
    loadFailed.value = true
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

watch([selectedModelId, dateRange, interval], loadAnalytics, { deep: true, immediate: true })
onMounted(loadModelsForFilter)
onUnmounted(() => { requestSequence += 1 })

function coverageLabel(observed: number, applicable: number): string {
  return t('modelSettings.analytics.observedCoverage', {
    observed: formatExactNumber(observed, locale.value),
    applicable: formatExactNumber(applicable, locale.value),
  })
}

function exactTitle(value: number | null): string {
  return value === null
    ? t('modelSettings.analytics.notObserved')
    : formatExactNumber(value, locale.value)
}

function exactLatencyTitle(value: number | null): string {
  return value === null
    ? t('modelSettings.analytics.notObserved')
    : `${formatExactNumber(value, locale.value)} ms`
}

function metricValue(bucket: ModelUsageAnalyticsBucket): number | null {
  if (trendMetric.value === 'calls') return bucket.calls.total
  if (trendMetric.value === 'tokens') return bucket.total_tokens.sum
  return bucket.latency.avg_ms
}

const chartWidth = 800
const chartHeight = 270
const plotLeft = 64
const plotRight = 18
const plotTop = 18
const plotBottom = 42
const plotBottomY = chartHeight - plotBottom

const chartMetricLabel = computed(() => {
  if (trendMetric.value === 'calls') return t('modelSettings.analytics.calls')
  if (trendMetric.value === 'tokens') return t('modelSettings.analytics.tokens')
  return t('modelSettings.analytics.latency')
})

function formatBucketLabel(bucketStart: string, includeYear = false): string {
  const options: Intl.DateTimeFormatOptions = interval.value === 'hour'
    ? { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'UTC' }
    : { month: 'short', day: 'numeric', timeZone: 'UTC' }
  if (includeYear) options.year = 'numeric'
  return new Intl.DateTimeFormat(locale.value, options).format(new Date(bucketStart))
}

function formatChartValue(value: number): string {
  if (trendMetric.value === 'latency') return `${formatExactNumber(value, locale.value)} ms`
  return formatExactNumber(value, locale.value)
}

function formatChartAxis(value: number): string {
  if (trendMetric.value === 'latency') return formatLatency(value, locale.value)
  return formatCompactNumber(value, locale.value)
}

const chartPoints = computed<ChartPoint[]>(() => {
  const buckets = analytics.value?.trend ?? []
  if (buckets.length === 0) return []
  const timestamps = buckets.map(bucket => Date.parse(bucket.bucket_start))
  const minTimestamp = Math.min(...timestamps)
  const maxTimestamp = Math.max(...timestamps)
  const values = buckets.map(metricValue)
  const observedValues = values.filter((value): value is number => value !== null)
  const maximum = Math.max(0, ...observedValues)
  const scaleMaximum = maximum === 0 ? 1 : maximum
  const plotWidth = chartWidth - plotLeft - plotRight
  const plotHeight = chartHeight - plotTop - plotBottom

  return buckets.map((bucket, index) => {
    const timestamp = timestamps[index]
    const x = minTimestamp === maxTimestamp
      ? plotLeft + plotWidth / 2
      : plotLeft + ((timestamp - minTimestamp) / (maxTimestamp - minTimestamp)) * plotWidth
    const value = values[index]
    const y = value === null ? null : plotTop + (1 - value / scaleMaximum) * plotHeight
    const valueLabel = value === null ? t('modelSettings.analytics.notObserved') : formatChartValue(value)
    return {
      bucketStart: bucket.bucket_start,
      timestamp,
      value,
      x,
      y,
      title: `${formatBucketLabel(bucket.bucket_start, true)} UTC · ${chartMetricLabel.value}: ${valueLabel}`,
    }
  })
})

const visibleChartPoints = computed(() => (
  chartPoints.value.filter((point): point is ChartPoint & { y: number; value: number } => (
    point.y !== null && point.value !== null
  ))
))

const chartHasData = computed(() => visibleChartPoints.value.length > 0)

const chartPaths = computed(() => {
  const paths: string[] = []
  let current: Array<ChartPoint & { y: number; value: number }> = []
  const expectedGap = interval.value === 'hour' ? 60 * 60 * 1000 : 24 * 60 * 60 * 1000
  const flush = () => {
    if (current.length > 1) {
      paths.push(current.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' '))
    }
    current = []
  }
  for (const point of chartPoints.value) {
    if (point.y === null || point.value === null) {
      flush()
      continue
    }
    const previous = current[current.length - 1]
    if (previous && point.timestamp - previous.timestamp > expectedGap * 1.5) flush()
    current.push(point as ChartPoint & { y: number; value: number })
  }
  flush()
  return paths
})

const chartYTicks = computed(() => {
  const maximum = Math.max(0, ...visibleChartPoints.value.map(point => point.value))
  const scaleMaximum = maximum === 0 ? 1 : maximum
  const plotHeight = chartHeight - plotTop - plotBottom
  return [scaleMaximum, scaleMaximum / 2, 0].map(value => ({
    value,
    y: plotTop + (1 - value / scaleMaximum) * plotHeight,
    label: formatChartAxis(value),
  }))
})

const chartXTicks = computed(() => {
  if (chartPoints.value.length === 0) return []
  const indexes = [...new Set([0, Math.floor((chartPoints.value.length - 1) / 2), chartPoints.value.length - 1])]
  return indexes.map((index, tickIndex) => {
    const point = chartPoints.value[index]
    return {
      bucketStart: point.bucketStart,
      x: point.x,
      label: formatBucketLabel(point.bucketStart),
      anchor: tickIndex === 0 ? 'start' : tickIndex === indexes.length - 1 ? 'end' : 'middle',
    } as const
  })
})

const chartAriaLabel = computed(() => (
  `${t('modelSettings.analytics.usageTrend')}: ${chartMetricLabel.value}, UTC`
))
</script>

<style scoped lang="less">
.usage-analytics {
  width: 100%;
}

.analytics-filters {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(280px, 1.5fr) auto;
  gap: 16px;
  align-items: end;
  padding: 18px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.analytics-filter {
  min-width: 0;

  > label {
    display: block;
    margin-bottom: 7px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 500;
  }

  :deep(.t-select-input),
  :deep(.t-date-range-picker) {
    width: 100%;
  }
}

:deep(.analytics-segmented.t-radio-group) {
  align-items: center;

  .t-radio-button,
  .t-radio-button.t-is-checked {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    height: calc(var(--td-comp-size-m) - 4px);
    margin: 0;
    padding: 0 var(--td-comp-paddingLR-l);
    border: 0;
    border-radius: var(--td-radius-small);
    line-height: 20px;
    vertical-align: middle;
  }

  &.t-size-s .t-radio-button {
    height: calc(var(--td-comp-size-xs) - 4px);
    padding: 0 var(--td-comp-paddingLR-s);
  }
}

.analytics-filter-warning {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 8px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.analytics-content {
  display: block;
  min-height: 320px;
  margin-top: 18px;
}

.analytics-loading-placeholder {
  min-height: 320px;
}

.analytics-state {
  display: flex;
  min-height: 320px;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 12px;
  color: var(--td-text-color-secondary);

  p {
    margin: 0;
  }
}

.analytics-state--error {
  color: var(--td-error-color);
}

.analytics-summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
}

.analytics-summary-card,
.analytics-panel {
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.analytics-summary-card {
  position: relative;
  min-width: 0;
  padding: 18px;
  overflow: hidden;
}

.summary-card__icon {
  position: absolute;
  top: 16px;
  right: 16px;
  display: inline-flex;
  width: 30px;
  height: 30px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
}

.summary-card__label,
.summary-card__value,
.summary-card__meta {
  margin: 0;
}

.summary-card__label {
  padding-right: 38px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.summary-card__value {
  margin-top: 13px;
  color: var(--td-text-color-primary);
  font-size: clamp(24px, 2.1vw, 31px);
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  line-height: 1.15;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary-card__meta {
  min-height: 36px;
  margin-top: 10px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 18px;
  overflow-wrap: anywhere;
}

.summary-card__meta--breakdown {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary-card__meta--tokens span {
  display: block;
}

.analytics-panel {
  padding: 20px;
}

.analytics-trend-panel {
  margin-top: 14px;
}

.analytics-panel__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  color: var(--td-text-color-secondary);

  h3,
  p {
    margin: 0;
  }

  h3 {
    color: var(--td-text-color-primary);
    font-size: 15px;
    font-weight: 600;

    span {
      color: var(--td-text-color-placeholder);
      font-size: 12px;
      font-weight: 500;
    }
  }

  p {
    margin-top: 5px;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
  }
}

.analytics-chart-wrap {
  width: 100%;
  margin-top: 18px;
  overflow: hidden;
}

.analytics-chart {
  display: block;
  width: 100%;
  min-height: 220px;
  color: var(--td-text-color-placeholder);
  font-family: inherit;
}

.chart-grid {
  line {
    stroke: var(--td-component-stroke);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }

  text {
    fill: var(--td-text-color-placeholder);
    font-size: 11px;
  }
}

.chart-axis {
  stroke: var(--td-text-color-disabled);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.chart-line {
  fill: none;
  stroke: var(--td-brand-color);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.25;
  vector-effect: non-scaling-stroke;
}

.chart-point-hit-area {
  fill: transparent;
}

.chart-point {
  fill: var(--td-brand-color);
  pointer-events: none;
}

.chart-point-target:hover .chart-point,
.chart-point-target:focus .chart-point {
  r: 4;
}

.chart-point-target:focus-visible {
  outline: 1px solid var(--td-brand-color);
}

.chart-x-labels text {
  fill: var(--td-text-color-placeholder);
  font-size: 11px;
}

.analytics-chart-empty {
  display: flex;
  min-height: 245px;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 10px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.analytics-cache-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.cache-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 22px;

  > div {
    padding: 14px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
  }

  span,
  strong {
    display: block;
  }

  span {
    color: var(--td-text-color-secondary);
    font-size: 12px;
  }

  strong {
    margin-top: 7px;
    color: var(--td-text-color-primary);
    font-size: 22px;
    font-variant-numeric: tabular-nums;
  }
}

.cache-metrics--single {
  grid-template-columns: minmax(0, 1fr);
}

.cache-panel__meta {
  margin: 14px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.5;
}

@media (max-width: 1100px) {
  .analytics-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .analytics-filters {
    grid-template-columns: 1fr;
    align-items: stretch;
  }

  .analytics-filter--interval :deep(.t-radio-group) {
    width: 100%;

    .t-radio-button {
      flex: 1;
    }
  }

  .analytics-trend-header {
    align-items: stretch;
    flex-direction: column;

    :deep(.t-radio-group) {
      align-self: flex-start;
    }
  }
}

@media (max-width: 560px) {
  .analytics-summary-grid,
  .analytics-cache-grid,
  .cache-metrics {
    grid-template-columns: 1fr;
  }

  .analytics-filters,
  .analytics-panel,
  .analytics-summary-card {
    padding: 16px;
  }

  .analytics-chart {
    min-width: 620px;
  }

  .analytics-chart-wrap {
    overflow-x: auto;
  }
}
</style>
