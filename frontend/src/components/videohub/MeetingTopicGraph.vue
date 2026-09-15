<template>
  <div class="graph-canvas-shell meeting-topic-graph-shell">
    <div ref="canvas" class="graph-canvas meeting-topic-graph" role="img" aria-label="会议主题簇关系网络" />
  </div>
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
const relationTypeLabels: Record<string, string> = {
  prerequisite: '前置依赖',
  conflict_constraint: '冲突约束',
  shared_support: '共同支撑',
  result_feedback: '结果反馈',
}

echarts.use([GraphChart, TooltipComponent, CanvasRenderer])
const props = defineProps<{ clusters: Cluster[]; relations: Relation[]; selectedId: string }>()
const emit = defineEmits<{ select: [clusterId: string] }>()
const canvas = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null

function color(token: string, fallback: string) { return readThemeToken(token) || fallback }
type LabelRect = { x: number; y: number; width: number; height: number }
function labelLayout() {
  return (params: { rect?: LabelRect; labelRect?: LabelRect }) => {
    const rect = params.rect
    const labelRect = params.labelRect
    const width = canvas.value?.clientWidth || 0
    const height = canvas.value?.clientHeight || 0
    if (!rect || !labelRect || !width || !height) return { hideOverlap: false, moveOverlap: 'shiftY' as const }
    return {
      x: Math.max(6, Math.min(rect.x + rect.width + 10, width - labelRect.width - 6)),
      y: Math.max(6, Math.min(rect.y + (rect.height - labelRect.height) / 2, height - labelRect.height - 6)),
      hideOverlap: false,
    }
  }
}

function render() {
  if (!chart) return
  const selectedNeighbors = new Set<string>()
  if (props.selectedId) props.relations.forEach(relation => {
    if (relation.source_cluster_id === props.selectedId) selectedNeighbors.add(relation.target_cluster_id)
    if (relation.target_cluster_id === props.selectedId) selectedNeighbors.add(relation.source_cluster_id)
  })
  const hasSelection = Boolean(props.selectedId)
  const nodes = props.clusters.map(cluster => ({
    id: cluster.cluster_id,
    name: cluster.title,
    symbol: 'circle',
    symbolSize: Math.min(42, 10 + Math.sqrt(cluster.source_video_ids.length + cluster.work_items.length + 1) * 9),
    itemStyle: {
      color: color('--color-data-1', color('--td-brand-color', '#2b7a56')),
      borderColor: 'rgba(255, 255, 255, .86)',
      borderWidth: props.selectedId === cluster.cluster_id ? 2 : 1,
      opacity: hasSelection && cluster.cluster_id !== props.selectedId && !selectedNeighbors.has(cluster.cluster_id) ? 0.18 : 1,
    },
    label: {
      show: true,
      width: 220,
      overflow: 'break',
      position: 'right',
      distance: 8,
      color: color('--td-text-color-primary', '#1f2a24'),
      fontSize: 12,
      lineHeight: 18,
      opacity: hasSelection && cluster.cluster_id !== props.selectedId && !selectedNeighbors.has(cluster.cluster_id) ? 0.16 : 1,
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
      value: relationTypeLabels[relation.relation_type] || relation.relation_type,
      lineStyle: {
        color: color('--td-text-color-secondary', '#728178'),
        width: relation.relation_type === 'conflict_constraint' ? 1.5 : 1.2,
        type: relation.relation_type === 'conflict_constraint' ? 'dashed' : 'solid',
        opacity: hasSelection && relation.source_cluster_id !== props.selectedId && relation.target_cluster_id !== props.selectedId ? 0.08 : 0.72,
      },
      symbol: relation.relation_type === 'prerequisite' || relation.relation_type === 'result_feedback' ? ['none', 'arrow'] : ['none', 'none'],
    }))
  chart.setOption({
    animationDurationUpdate: 360,
    tooltip: {
      trigger: 'item',
      renderMode: 'richText',
      confine: true,
      backgroundColor: 'rgba(255, 255, 255, .9)',
      borderColor: '#ffffff',
      borderWidth: 1,
      padding: [6, 9],
      textStyle: { color: color('--td-text-color-primary', '#1f2a24'), fontSize: 12 },
      formatter: (params: { data?: { name?: string; id?: string } }) => params.data?.name || params.data?.id || '',
    },
    series: [{
      type: 'graph',
      layout: 'force',
      roam: true,
      draggable: true,
      left: '7%',
      right: '7%',
      top: '9%',
      bottom: '9%',
      scaleLimit: { min: 0.4, max: 3 },
      data: nodes,
      links,
      edgeSymbolSize: 8,
      edgeSymbol: ['none', 'arrow'],
      edgeLabel: {
        show: true,
        position: 'middle',
        color: color('--td-text-color-primary', '#1f2a24'),
        fontSize: 10,
        fontWeight: 500,
        backgroundColor: 'rgba(255,255,255,.88)',
        textBorderColor: 'rgba(255,255,255,.94)',
        textBorderWidth: 2,
        padding: [2, 4],
        borderRadius: 3,
        opacity: 0.96,
        formatter: (params: { data?: { value?: string } }) => params.data?.value || '',
      },
      force: { repulsion: 320, edgeLength: [92, 168], gravity: 0.08, friction: 0.58 },
      labelLayout: labelLayout(),
      lineStyle: { color: color('--td-text-color-secondary', '#728178') },
      emphasis: {
        focus: 'adjacency',
        blurScope: 'coordinateSystem',
        scale: 1.45,
        label: { show: true, color: color('--td-text-color-primary', '#1f2a24'), fontSize: 12, fontWeight: 500 },
        edgeLabel: { show: true, opacity: 1 },
        itemStyle: { borderColor: '#ffffff', borderWidth: 2 },
        lineStyle: { opacity: 1, width: 2.2 },
      },
      blur: { itemStyle: { opacity: 0.18 }, lineStyle: { opacity: 0.06 }, label: { opacity: 0.12 }, edgeLabel: { show: true, opacity: 0.72 } },
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
.graph-canvas-shell, .meeting-topic-graph-shell { position: relative; min-width: 0; overflow: hidden; border: 1px solid rgba(255,255,255,.82); border-radius: var(--td-radius-extraLarge); background: rgba(232,239,236,.42); box-shadow: inset 0 1px 0 rgba(255,255,255,.72); backdrop-filter: blur(24px) saturate(112%); -webkit-backdrop-filter: blur(24px) saturate(112%); }
.graph-canvas, .meeting-topic-graph { display: block; width: 100%; height: max(520px, calc(100vh - 210px)); background: linear-gradient(145deg, rgba(255,255,255,.12), rgba(218,230,225,.22)); }
@media (max-width: 640px) { .meeting-topic-graph { height: max(520px, calc(100vh - 236px)); } }
</style>
