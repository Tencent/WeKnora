<template>
  <div ref="canvas" class="graph-canvas" role="img" aria-label="知识节点关系图" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { GraphChart } from 'echarts/charts'
import { TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { FALLBACK_ATTRIBUTE_COLOR, FALLBACK_RELATION_STYLE, KNOWN_ATTRIBUTES, KNOWN_RELATION_TYPES, readThemeToken } from './graphStyles'
import { getRelationTypeLabel } from './knowledgeTypeStyles'
import type { GraphEdge, GraphNode, GraphReadingAssociation } from '@/types/videohub'

echarts.use([GraphChart, TooltipComponent, CanvasRenderer])
const props = defineProps<{ nodes: GraphNode[]; edges: GraphEdge[]; readingAssociations?: GraphReadingAssociation[] }>()
const emit = defineEmits<{ nodeClick: [node: GraphNode] }>()
const canvas = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null

function color(token: string) { return readThemeToken(token) }
function escapeHtml(value: string) { return value.replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[char]!) }
function render() {
  if (!chart) return
  const nodeById = new Map(props.nodes.map(node => [node.id, node]))
  chart.setOption({
    animationDurationUpdate: 300,
    tooltip: {
      trigger: 'item', backgroundColor: color('--td-bg-color-container'),
      borderColor: color('--td-component-stroke'), borderWidth: 1,
      textStyle: { color: color('--td-text-color-primary') },
      formatter: (params: { dataType?: string; data?: { id?: string } }) => {
        if (params.dataType !== 'node' || !params.data?.id) return ''
        const node = nodeById.get(params.data.id)
        if (!node) return ''
        return [node.label, node.attributes[0] || '无分类', node.video_title || '未关联视频'].map(escapeHtml).join('<br/>')
      },
    },
    series: [{
      type: 'graph', layout: 'force', roam: true, draggable: true,
      scaleLimit: { min: .3, max: 3 }, symbol: 'circle', symbolSize: 24,
      force: { repulsion: 200, edgeLength: 100 },
      label: { show: true, position: 'right', color: color('--td-text-color-primary'), fontSize: 12 },
      emphasis: { focus: 'adjacency', scale: 1.25, lineStyle: { opacity: 1 } },
      blur: { itemStyle: { opacity: .2 }, lineStyle: { opacity: .08 }, label: { opacity: .2 } },
      data: props.nodes.map(node => ({
        id: node.id, name: node.label,
        symbolSize: Math.min(52, 22 + Math.sqrt(node.link_count ?? 0) * 6),
        itemStyle: { color: color(KNOWN_ATTRIBUTES[node.type || node.attributes[0]] ?? FALLBACK_ATTRIBUTE_COLOR) },
      })),
      links: [
        ...props.edges.map(edge => {
          const style = KNOWN_RELATION_TYPES[edge.type] ?? FALLBACK_RELATION_STYLE
          return { source: edge.source, target: edge.target, value: getRelationTypeLabel(edge.type), lineStyle: { type: style.lineStyle, width: style.width, opacity: style.opacity, color: color(style.color) } }
        }),
        ...(props.readingAssociations ?? []).filter(edge => edge.target_exists).map(edge => ({
          source: edge.source, target: edge.target, value: '阅读关联',
          lineStyle: { type: 'dashed', width: 1, opacity: .5, color: color('--td-text-color-placeholder') },
        })),
      ],
      lineStyle: { curveness: .08 },
    }],
  }, true)
}

onMounted(() => {
  if (!canvas.value) return
  chart = echarts.init(canvas.value)
  chart.on('click', params => {
    if (params.dataType !== 'node') return
    const id = (params.data as { id?: string })?.id
    const node = props.nodes.find(item => item.id === id)
    if (node) emit('nodeClick', node)
  })
  resizeObserver = new ResizeObserver(() => chart?.resize())
  resizeObserver.observe(canvas.value)
  themeObserver = new MutationObserver(render)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'theme-mode'] })
  render()
})
watch(() => [props.nodes, props.edges, props.readingAssociations], render, { deep: true })
onBeforeUnmount(() => { resizeObserver?.disconnect(); themeObserver?.disconnect(); chart?.dispose(); chart = null })
</script>

<style scoped>
.graph-canvas { width: 100%; height: max(520px, calc(100vh - 210px)); border: 1px solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
</style>
