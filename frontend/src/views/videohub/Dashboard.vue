<template>
  <main class="dashboard-page">
    <header class="dashboard-page__header">
      <div><h1>User Queries</h1><p>了解视频内容的提问趋势与高频问题</p></div>
      <RangeSelector :model-value="range" :custom="customRange" @update:model-value="changeRange" @update:custom="changeCustom" @invalid="showWarning" />
    </header>

    <div v-if="loading" class="dashboard-page__state"><t-loading text="正在加载数据看板" /></div>
    <div v-else-if="error" class="dashboard-page__state"><t-empty :description="error" /><t-button theme="primary" @click="loadFromRoute">重新加载</t-button></div>
    <template v-else-if="payload">
      <section class="dashboard-page__kpis" aria-label="核心指标">
        <KpiCard title="总提问数" :value="payload.kpi.total_questions" :trend="{ value: payload.kpi.trend.total_questions }" :tooltip="`所选时段内用户提交的问题总数 · ${periodLabel}`" />
        <KpiCard title="活跃视频数" :value="payload.kpi.active_videos" :trend="{ value: payload.kpi.trend.active_videos }" :tooltip="`至少被提问一次的视频数量 · ${periodLabel}`" />
        <KpiCard title="高频问题" :value="payload.kpi.cluster_count" :trend="{ value: payload.kpi.trend.cluster_count }" :tooltip="`语义相近问题聚合后的主题数量 · ${periodLabel}`" />
        <KpiCard title="平均问题" :value="payload.kpi.avg_questions_per_video" :trend="{ value: payload.kpi.trend.avg_questions_per_video }" :tooltip="`总提问数除以活跃视频数 · ${periodLabel}`" />
      </section>
      <section class="dashboard-card">
        <header><div><h2>提问趋势</h2><p>每日提问数量及热门视频</p></div></header>
        <QuestionTrendChart v-if="payload.trend.length" :points="payload.trend" />
        <t-empty v-else description="当前时间范围暂无提问趋势" />
      </section>
      <section class="dashboard-card dashboard-card--table">
        <header><div><h2>高频问题</h2><p>点击代表性问题查看关联视频</p></div></header>
        <QuestionClustersTable :clusters="payload.clusters" @select-video-by-id="openVideo" />
      </section>
    </template>
    <div v-else class="dashboard-page__state"><t-empty description="暂无看板数据，请先上传并分析视频" /></div>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { fetchDashboard } from '@/api/videohub/dashboard'
import KpiCard from '@/components/videohub/KpiCard.vue'
import QuestionClustersTable from '@/components/videohub/QuestionClustersTable.vue'
import QuestionTrendChart from '@/components/videohub/QuestionTrendChart.vue'
import RangeSelector from '@/components/videohub/RangeSelector.vue'
import type { DashboardPayload, DashboardRange } from '@/types/videohub'

const route = useRoute()
const router = useRouter()
const allowedRanges = new Set<DashboardRange>(['7d', '30d', '90d', 'custom'])
const range = ref<DashboardRange>('7d')
const customRange = ref<[string, string] | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const payload = ref<DashboardPayload | null>(null)
const periodLabel = computed(() => payload.value?.from && payload.value?.to ? `${payload.value.from} 至 ${payload.value.to}` : range.value === 'custom' ? '自定义时段' : `近 ${range.value.slice(0, -1)} 天`)

function dayNumber(value: string) { const [year, month, day] = value.split('-').map(Number); return Date.UTC(year, month - 1, day) }
function localToday() {
  const now = new Date()
  return Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())
}
function isDate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  return new Date(dayNumber(value)).toISOString().slice(0, 10) === value
}
function validCustom(from: unknown, to: unknown): from is string {
  if (!isDate(from) || !isDate(to)) return false
  const days = (dayNumber(to) - dayNumber(from)) / 86400000 + 1
  return days >= 1 && days <= 90 && dayNumber(to) <= localToday()
}
function parseQuery() {
  const candidate = typeof route.query.range === 'string' && allowedRanges.has(route.query.range as DashboardRange) ? route.query.range as DashboardRange : '7d'
  if (candidate === 'custom' && validCustom(route.query.from, route.query.to)) return { range: candidate, custom: [route.query.from, route.query.to as string] as [string, string] }
  return { range: candidate === 'custom' ? '7d' as const : candidate, custom: null }
}
async function loadFromRoute() {
  const parsed = parseQuery(); range.value = parsed.range; customRange.value = parsed.custom
  if (route.query.range === 'custom' && parsed.range !== 'custom') {
    await router.replace({ query: { ...route.query, range: '7d', from: undefined, to: undefined } })
    return
  }
  loading.value = true; error.value = null
  try { payload.value = await fetchDashboard({ range: parsed.range, from: parsed.custom?.[0], to: parsed.custom?.[1] }) }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败，请稍后重试'; payload.value = null }
  finally { loading.value = false }
}
function changeRange(value: DashboardRange) {
  range.value = value
  if (value === 'custom') { customRange.value = null; return }
  router.replace({ query: { ...route.query, range: value, from: undefined, to: undefined } })
}
function changeCustom(value: [string, string] | null) {
  customRange.value = value
  if (!value) return
  router.replace({ query: { ...route.query, range: 'custom', from: value[0], to: value[1] } })
}
function showWarning(message: string) { MessagePlugin.warning(message) }
function openVideo(videoId: string, seconds: number) { const href = router.resolve({ name: 'videoDetail', params: { videoId }, query: { t: Math.max(0, Math.floor(seconds)) } }).href; window.open(href, '_blank', 'noopener,noreferrer') }
watch(() => [route.query.range, route.query.from, route.query.to], loadFromRoute, { immediate: true })
</script>

<style scoped>
.dashboard-page { display: grid; align-content: start; gap: calc(var(--td-comp-margin-s) * 2.5); height: 100%; overflow-y: auto; padding: calc(var(--td-comp-margin-s) * 3); background: var(--td-bg-color-container); }
.dashboard-page__header { display: flex; align-items: flex-end; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 2); }
.dashboard-page h1, .dashboard-page h2 { margin: 0; color: var(--td-text-color-primary); }
.dashboard-page h1 { font-size: var(--td-font-size-headline-medium); }
.dashboard-page h2 { font-size: var(--td-font-size-title-large); }
.dashboard-page__header p, .dashboard-card header p { margin: calc(var(--td-comp-margin-s) / 2) 0 0; color: var(--td-text-color-secondary); }
.dashboard-page__kpis { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: calc(var(--td-comp-margin-s) * 2); }
.dashboard-card { padding: calc(var(--td-comp-margin-s) * 2.5); border: 1px solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.dashboard-card > header { margin-bottom: calc(var(--td-comp-margin-s) * 2); }
.dashboard-card--table { min-width: 0; }
.dashboard-page__state { display: grid; place-items: center; min-height: 420px; gap: var(--td-comp-margin-s); }
@media (max-width: 1000px) { .dashboard-page__kpis { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 720px) { .dashboard-page { padding: calc(var(--td-comp-margin-s) * 2); } .dashboard-page__header { align-items: stretch; flex-direction: column; } .dashboard-page__kpis { grid-template-columns: 1fr; } }
</style>
