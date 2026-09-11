<template>
  <div class="graph-canvas-shell">
    <div ref="canvas" class="graph-canvas" role="img" aria-label="知识节点粒子关系图" />
    <ul class="graph-canvas__legend" aria-label="五类知识对象图例">
      <li v-for="item in legendItems" :key="item.label">
        <span class="graph-canvas__legend-dot" :style="{ background: item.colorVar }" />
        <span>{{ item.label }}</span>
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { GraphChart } from 'echarts/charts'
import { TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { FALLBACK_ATTRIBUTE_COLOR, FALLBACK_RELATION_STYLE, KNOWN_ATTRIBUTES, KNOWN_RELATION_TYPES, readThemeToken } from './graphStyles'
import { getRelationTypeLabel, KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { GraphEdge, GraphNode, GraphReadingAssociation } from '@/types/videohub'

echarts.use([GraphChart, TooltipComponent, CanvasRenderer])
const props = defineProps<{ nodes: GraphNode[]; edges: GraphEdge[]; readingAssociations?: GraphReadingAssociation[] }>()
const emit = defineEmits<{ nodeClick: [node: GraphNode] }>()
const legendItems = KNOWLEDGE_TYPES.map(type => KNOWLEDGE_TYPE_STYLES[type])
const canvas = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null

function color(token: string) { return readThemeToken(token) }
function render() {
  if (!chart) return
  const links = new Map<string, { source: string; target: string; value: string; tooltip: { show: boolean }; lineStyle: { type: string; width: number; opacity: number; color: string } }>()
  const addLink = (source: string, target: string, value: string, lineStyle: { type: string; width: number; opacity: number; color: string }) => {
    const key = [source, target].sort().join('\u0000')
    const existing = links.get(key)
    if (existing) {
      if (existing.value !== value && !existing.value.includes(value)) existing.value = `${existing.value} · ${value}`
      if (existing.lineStyle.type === 'dashed' && lineStyle.type !== 'dashed') existing.lineStyle = lineStyle
      return
    }
    links.set(key, { source, target, value, tooltip: { show: false }, lineStyle })
  }
  chart.setOption({
    animationDurationUpdate: 300,
    tooltip: {
      trigger: 'item', renderMode: 'richText', confine: true,
      backgroundColor: 'rgba(255, 255, 255, .9)', borderColor: '#ffffff', borderWidth: 1,
      padding: [6, 9], textStyle: { color: color('--td-text-color-primary'), fontSize: 12 },
    },
    series: [{
      type: 'graph', layout: 'force', roam: true, draggable: true,
      scaleLimit: { min: .4, max: 3 }, symbol: 'circle', symbolSize: 16,
      force: { repulsion: 320, edgeLength: [92, 168], gravity: .08, friction: .58 },
      label: { show: true, position: 'right', distance: 8, width: 128, overflow: 'truncate', color: color('--td-text-color-primary'), fontSize: 11 },
      labelLayout: { hideOverlap: false, moveOverlap: 'shiftY' },
      emphasis: {
        focus: 'adjacency', blurScope: 'coordinateSystem', scale: 1.45,
        label: { show: true, color: color('--td-text-color-primary'), fontSize: 12, fontWeight: 500 },
        itemStyle: { borderColor: '#ffffff', borderWidth: 2 },
        lineStyle: { opacity: 1, width: 2.2 },
      },
      blur: { itemStyle: { opacity: .18 }, lineStyle: { opacity: .06 }, label: { opacity: .12 } },
      data: props.nodes.map(node => {
        const nodeColor = color(KNOWN_ATTRIBUTES[node.type || node.attributes[0]] ?? FALLBACK_ATTRIBUTE_COLOR)
        return {
          id: node.id, name: node.label,
          symbolSize: Math.min(42, 10 + Math.sqrt((node.link_count ?? 0) + 1) * 9),
          tooltip: { show: true, formatter: node.label },
          itemStyle: {
            color: nodeColor,
            borderColor: 'rgba(255, 255, 255, .86)',
            borderWidth: 1,
          },
        }
      }),
      links: (() => {
        props.edges.forEach(edge => {
          const style = KNOWN_RELATION_TYPES[edge.type] ?? FALLBACK_RELATION_STYLE
          addLink(edge.source, edge.target, getRelationTypeLabel(edge.type), { type: style.lineStyle, width: Math.max(1.5, Math.min(style.width, 2)), opacity: Math.max(.68, style.opacity), color: color('--td-text-color-secondary') })
        }),
        (props.readingAssociations ?? []).filter(edge => edge.target_exists).forEach(edge => addLink(edge.source, edge.target, '延伸关系', { type: 'dashed', width: 1.5, opacity: .58, color: color('--td-text-color-secondary') }))
        return [...links.values()]
      })(),
      lineStyle: { curveness: .06, color: color('--td-text-color-secondary'), opacity: .68 },
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
.graph-canvas-shell { position: relative; min-width: 0; overflow: hidden; border: 1px solid rgba(255,255,255,.82); border-radius: var(--td-radius-extraLarge); background: rgba(232,239,236,.42); box-shadow: inset 0 1px 0 rgba(255,255,255,.72); backdrop-filter: blur(24px) saturate(112%); -webkit-backdrop-filter: blur(24px) saturate(112%); }
.graph-canvas { display: block; width: 100%; height: max(520px, calc(100vh - 210px)); background: linear-gradient(145deg, rgba(255,255,255,.12), rgba(218,230,225,.22)); }
.graph-canvas__legend { position: absolute; z-index: 2; top: 12px; right: 12px; display: flex; max-width: calc(100% - 24px); flex-wrap: wrap; gap: 6px 12px; margin: 0; padding: 7px 9px; border: 1px solid rgba(255,255,255,.82); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.48); backdrop-filter: blur(18px) saturate(112%); -webkit-backdrop-filter: blur(18px) saturate(112%); color: var(--td-text-color-secondary); font-size: 11px; line-height: 16px; list-style: none; pointer-events: none; }
.graph-canvas__legend li { display: inline-flex; align-items: center; gap: 5px; white-space: nowrap; }
.graph-canvas__legend-dot { display: inline-block; width: 8px; height: 8px; flex: none; border: 1px solid rgba(255,255,255,.86); border-radius: var(--td-radius-circle); }
@media (max-width: 640px) { .graph-canvas__legend { right: 8px; left: 8px; justify-content: center; max-width: none; }.graph-canvas { height: max(520px, calc(100vh - 236px)); } }
</style>
