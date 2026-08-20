<template><div ref="canvas" class="trend-chart" role="img" aria-label="每日提问趋势折线图" /></template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { QuestionTrendPoint } from '@/types/videohub'

echarts.use([LineChart, GridComponent, TooltipComponent, CanvasRenderer])
const props = defineProps<{ points: QuestionTrendPoint[] }>()
const canvas = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null
function token(name: string) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() }
function escapeHtml(value: string) { return value.replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[char]!) }
function render() {
  if (!chart) return
  chart.setOption({
    grid: { left: 44, right: 18, top: 24, bottom: 34 },
    tooltip: { trigger: 'axis', backgroundColor: token('--td-bg-color-container'), borderColor: token('--td-component-stroke'), textStyle: { color: token('--td-text-color-primary') }, formatter: (items: Array<{ dataIndex: number }>) => { const point = props.points[items[0]?.dataIndex ?? 0]; if (!point) return ''; return [`${escapeHtml(point.date)} · ${point.count} 次提问`, ...point.top_videos.map((video, index) => `${index + 1}. ${escapeHtml(video.title)} ${video.count}`)].join('<br/>') } },
    xAxis: { type: 'category', boundaryGap: false, data: props.points.map(point => point.date.slice(5)), axisLabel: { color: token('--td-text-color-secondary') }, axisLine: { lineStyle: { color: token('--td-component-stroke') } } },
    yAxis: { type: 'value', minInterval: 1, axisLabel: { color: token('--td-text-color-secondary') }, splitLine: { lineStyle: { color: token('--td-component-stroke'), opacity: .6 } } },
    series: [{ type: 'line', smooth: true, showSymbol: props.points.length <= 30, symbolSize: 7, data: props.points.map(point => point.count), lineStyle: { width: 3, color: token('--td-brand-color') }, itemStyle: { color: token('--td-brand-color') }, areaStyle: { color: token('--td-brand-color-light'), opacity: .35 }, emphasis: { focus: 'series' } }],
  }, true)
}
onMounted(() => { if (!canvas.value) return; chart = echarts.init(canvas.value); resizeObserver = new ResizeObserver(() => chart?.resize()); resizeObserver.observe(canvas.value); themeObserver = new MutationObserver(render); themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'theme-mode'] }); render() })
watch(() => props.points, render, { deep: true })
onBeforeUnmount(() => { resizeObserver?.disconnect(); themeObserver?.disconnect(); chart?.dispose(); chart = null })
</script>

<style scoped>.trend-chart { width: 100%; height: 320px; }</style>
