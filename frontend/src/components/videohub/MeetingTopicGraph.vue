<template>
  <div ref="canvas" class="meeting-topic-graph" role="img" aria-label="会议主题簇关系网络" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { GraphChart } from 'echarts/charts'
import { TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { readThemeToken } from './graphStyles'

type Cluster = { cluster_id: string; title: string; source_video_ids: string[]; work_items: Array<unknown> }
type Relation = { relation_id: string; source_cluster_id: string; target_cluster_id: string; relation_type: string; summary: string }

echarts.use([GraphChart, TooltipComponent, CanvasRenderer])
const props = defineProps<{ clusters: Cluster[]; relations: Relation[]; selectedId: string }>()
const emit = defineEmits<{ select: [clusterId: string] }>()
const canvas = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null

function color(token: string, fallback: string) { return readThemeToken(token) || fallback }

function render() {
  if (!chart) return
  const width = canvas.value?.clientWidth || 640
  const height = canvas.value?.clientHeight || 470
  const total = Math.max(props.clusters.length, 1)
  const columns = total > 3 ? 2 : total
  const rows = Math.ceil(total / columns)
  const insetX = Math.min(96, Math.max(52, width * 0.12))
  const insetY = 54
  const plotWidth = Math.max(width - insetX * 2, 160)
  const plotHeight = Math.max(height - insetY * 2 - (rows > 1 ? 28 : 0), 160)
  const nodes = props.clusters.map((cluster, index) => ({
    id: cluster.cluster_id,
    name: cluster.title,
    x: insetX + ((index % columns) + 0.5) * (plotWidth / columns),
    y: insetY + (Math.floor(index / columns) + 0.5) * (plotHeight / rows),
    symbol: 'roundRect',
    symbolSize: [176, 76],
    itemStyle: {
      color: props.selectedId === cluster.cluster_id ? color('--td-brand-color-1', '#d8e9df') : color('--td-bg-color-container', '#ffffff'),
      borderColor: props.selectedId === cluster.cluster_id ? color('--td-brand-color', '#2b7a56') : color('--td-component-stroke', '#d6dfda'),
      borderWidth: props.selectedId === cluster.cluster_id ? 2 : 1,
      shadowBlur: 10,
      shadowColor: 'rgba(52, 79, 65, .12)',
    },
    label: {
      show: true,
      width: 146,
      overflow: 'truncate',
      color: color('--td-text-color-primary', '#1f2a24'),
      fontSize: 12,
      lineHeight: 18,
      formatter: `${cluster.title}\n${cluster.source_video_ids.length} 场视频 · ${cluster.work_items.length} 项事项`,
    },
  }))
  const clusterIds = new Set(props.clusters.map(cluster => cluster.cluster_id))
  const links = props.relations
    .filter(relation => clusterIds.has(relation.source_cluster_id) && clusterIds.has(relation.target_cluster_id))
    .map(relation => ({
      id: relation.relation_id,
      source: relation.source_cluster_id,
      target: relation.target_cluster_id,
      name: relation.summary,
      lineStyle: {
        color: color('--td-text-color-secondary', '#728178'),
        width: relation.relation_type === 'conflict_constraint' ? 1.5 : 1.2,
        type: relation.relation_type === 'conflict_constraint' ? 'dashed' : 'solid',
        opacity: 0.72,
      },
      symbol: relation.relation_type === 'prerequisite' || relation.relation_type === 'result_feedback' ? ['none', 'arrow'] : ['none', 'none'],
    }))
  chart.setOption({
    animationDurationUpdate: 240,
    tooltip: { trigger: 'item', confine: true, formatter: (params: { data?: { name?: string; id?: string } }) => params.data?.name || params.data?.id || '' },
    series: [{
      type: 'graph',
      layout: 'none',
      roam: false,
      draggable: false,
      left: 0,
      right: 0,
      top: 0,
      bottom: 0,
      data: nodes,
      links,
      edgeSymbolSize: 8,
      lineStyle: { color: color('--td-text-color-secondary', '#728178') },
      emphasis: { focus: 'adjacency', lineStyle: { width: 2, opacity: 1 } },
    }],
  }, true)
}

onMounted(() => {
  if (!canvas.value) return
  chart = echarts.init(canvas.value)
  chart.on('click', params => {
    if (params.dataType !== 'node') return
    const id = (params.data as { id?: string })?.id
    if (id) emit('select', id)
  })
  resizeObserver = new ResizeObserver(() => { chart?.resize(); render() })
  resizeObserver.observe(canvas.value)
  themeObserver = new MutationObserver(render)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'theme-mode'] })
  render()
})
watch(() => [props.clusters, props.relations, props.selectedId], render, { deep: true })
onBeforeUnmount(() => { resizeObserver?.disconnect(); themeObserver?.disconnect(); chart?.dispose(); chart = null })
</script>

<style scoped>
.meeting-topic-graph { width: 100%; height: 470px; }
@media (max-width: 680px) { .meeting-topic-graph { height: 420px; } }
</style>
