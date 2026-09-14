<template>
  <section class="training-graph-prototype" aria-label="培训主题知识图谱原型">
    <div class="training-prototype__notice">
      <span class="training-prototype__eyebrow">PROTOTYPE · 只读预览</span>
      <strong>培训主题簇 · 知识图谱映射</strong>
      <span>用于验证主题数量增长时，关系、标题和学习路径是否仍然可读。</span>
    </div>

    <div v-if="variant.key === 'native'" class="prototype-surface prototype-surface--native">
      <header class="prototype-surface__head"><div><span class="surface-kicker">VIEW 01</span><h2>原生力导图</h2><p>粒子大小映射视频数量，画布只保留主题名称；箭头方向表示学习先后，标签保留培训关系语义。</p></div><div class="surface-metrics"><strong>{{ nodes.length }}</strong><span>主题簇</span><strong>{{ totalVideos }}</strong><span>视频</span></div></header>
      <GraphCanvas :nodes="graphNodes" :edges="graphEdges" :selected-node-id="selectedId" :full-labels="true" :show-legend="false" :relation-labels="relationLabels" :show-relation-labels="true" :show-relation-arrows="true" @node-click="selectNode" />
    </div>

    <div v-else-if="variant.key === 'inspector'" class="prototype-surface prototype-surface--inspector">
      <header class="prototype-surface__head"><div><span class="surface-kicker">VIEW 02</span><h2>图谱 + 主题详情</h2><p>左侧保持图谱语义，右侧承接培训场景的学习信息。</p></div><span class="surface-status">当前选中 · {{ selectedNode.title }}</span></header>
      <div class="inspector-layout"><GraphCanvas :nodes="graphNodes" :edges="graphEdges" :selected-node-id="selectedId" :full-labels="true" :show-legend="false" :relation-labels="relationLabels" :show-relation-labels="true" :show-relation-arrows="true" @node-click="selectNode" /><aside class="prototype-inspector"><span class="surface-kicker">SELECTED TOPIC</span><div class="inspector-node"><span class="node-dot" :style="{ '--node-color': selectedNode.color, background: selectedNode.color }" /><div><strong>{{ selectedNode.title }}</strong><small>{{ selectedNode.videos }} 个视频 · {{ selectedNode.units }} 个学习单元</small></div></div><p>{{ selectedNode.description }}</p><div class="inspector-section"><span>学习目标</span><strong>{{ selectedNode.goal }}</strong></div><div class="inspector-section"><span>关系</span><button v-for="edge in selectedEdges" :key="edge.id" type="button" class="relation-row" @click="selectNode(edge.otherId)"><b>{{ edge.label }}</b><span>{{ edge.summary }}</span></button></div></aside></div>
    </div>

    <div v-else class="prototype-surface prototype-surface--path">
      <header class="prototype-surface__head"><div><span class="surface-kicker">VIEW 03</span><h2>学习路径流</h2><p>把关系方向转译成从基础到应用的可扫描路径。</p></div><div class="path-legend"><span v-for="item in relationLegend" :key="item.label"><i :style="{ background: item.color }" />{{ item.label }}</span></div></header>
      <div class="learning-path"><div v-for="(node, index) in pathNodes" :key="node.id" class="path-column"><div v-if="index" class="path-connector"><span>{{ pathNodes[index - 1].pathLabel }}</span></div><button type="button" class="path-node" :class="{ 'is-selected': selectedId === node.id }" @click="selectNode(node.id)"><span class="path-node__index">{{ String(index + 1).padStart(2, '0') }}</span><span class="path-node__dot" :style="{ '--node-color': node.color, background: node.color, width: `${16 + node.videos * 4}px`, height: `${16 + node.videos * 4}px` }" /><strong>{{ node.title }}</strong><em>{{ node.description }}</em></button></div></div>
    </div>

    <nav class="prototype-switcher" aria-label="原型方案切换"><button type="button" title="上一个方案" aria-label="上一个方案" @click="cycle(-1)"><ChevronLeftIcon /></button><span><b>{{ variant.key.toUpperCase() }}</b><strong>{{ variant.name }}</strong></span><button type="button" title="下一个方案" aria-label="下一个方案" @click="cycle(1)"><ChevronRightIcon /></button></nav>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ChevronLeftIcon, ChevronRightIcon } from 'tdesign-icons-vue-next'
import { useRoute, useRouter } from 'vue-router'
import GraphCanvas from './GraphCanvas.vue'
import type { GraphEdge, GraphNode } from '@/types/videohub'

type VariantKey = 'native' | 'inspector' | 'path'
type TrainingNode = { id: string; title: string; type: string; units: number; videos: number; description: string; goal: string; color: string; x: number; y: number; pathLabel?: string }
type TrainingEdge = { id: string; source: string; target: string; label: string; summary: string; color: string }

const router = useRouter(); const route = useRoute()
const variants: Array<{ key: VariantKey; name: string }> = [{ key: 'native', name: '原生力导图' }, { key: 'inspector', name: '图谱 + 详情' }, { key: 'path', name: '学习路径流' }]
const nodes: TrainingNode[] = [
  { id: 'n1', title: '问题定义与目标拆解', type: '概念认知', units: 4, videos: 3, description: '先明确问题边界、成功标准和约束条件，再进入方法选择。', goal: '能将模糊需求拆成可验证的学习目标。', color: 'var(--color-data-2)', x: 18, y: 27, pathLabel: '必须先学' },
  { id: 'n2', title: '用户研究与证据收集', type: '方法论', units: 5, videos: 5, description: '围绕用户、场景与证据建立事实基础，避免凭经验决策。', goal: '能设计访谈、观察和证据记录流程。', color: 'var(--color-data-4)', x: 46, y: 18, pathLabel: '推荐先学' },
  { id: 'n3', title: '第一性原理分析框架', type: '方法论', units: 6, videos: 7, description: '从目标和基本约束出发，重建问题的因果链与判断标准。', goal: '能用基本事实推导方案，而不是复述结论。', color: 'var(--color-data-1)', x: 71, y: 31, pathLabel: '用于实践' },
  { id: 'n4', title: '知识图谱建模与关系设计', type: '技能方法', units: 7, videos: 4, description: '将实体、概念、案例、方法论和洞察组织成可导航的关系网络。', goal: '能定义节点、关系和展示层的最小闭环。', color: 'var(--color-data-3)', x: 30, y: 67, pathLabel: '补充理解' },
  { id: 'n5', title: '培训路径编排与分层呈现', type: '技能方法', units: 5, videos: 6, description: '把知识关系转译为学习顺序、阶段和可执行任务。', goal: '能让不同基础的学习者快速找到下一步。', color: 'var(--color-data-5)', x: 62, y: 69, pathLabel: '用于实践' },
  { id: 'n6', title: '案例复盘：从洞察到行动', type: '案例分析', units: 3, videos: 2, description: '以真实案例验证模型是否能指导行动，并暴露知识缺口。', goal: '能通过案例完成一次闭环复盘。', color: 'var(--color-data-3)', x: 85, y: 57, pathLabel: '对照理解' },
]
const edges: TrainingEdge[] = [
  { id: 'e1', source: 'n1', target: 'n2', label: '必须先学', summary: '先建立问题与证据边界', color: 'var(--color-data-2)' },
  { id: 'e2', source: 'n2', target: 'n3', label: '推荐先学', summary: '证据支撑原理分析', color: 'var(--color-data-4)' },
  { id: 'e3', source: 'n3', target: 'n4', label: '用于实践', summary: '把分析框架落到关系设计', color: 'var(--color-data-1)' },
  { id: 'e4', source: 'n4', target: 'n5', label: '补充理解', summary: '模型支撑学习路径编排', color: 'var(--color-data-3)' },
  { id: 'e5', source: 'n5', target: 'n6', label: '用于实践', summary: '用案例验证学习成果', color: 'var(--color-data-5)' },
  { id: 'e6', source: 'n1', target: 'n4', label: '补充理解', summary: '目标定义影响建模粒度', color: 'var(--color-data-2)' },
]
const relationLegend = [{ label: '必须先学', color: 'var(--color-data-2)' }, { label: '推荐先学', color: 'var(--color-data-4)' }, { label: '用于实践', color: 'var(--color-data-1)' }]
const relationLabels = Object.fromEntries(edges.map(edge => [edge.id, edge.label]))
const selectedId = ref('n3')
const totalVideos = computed(() => nodes.reduce((total, node) => total + node.videos, 0))
// Display-only adapter: training topics borrow the native renderer shape without entering the formal graph model.
const graphNodes = computed<GraphNode[]>(() => nodes.map(node => ({ id: node.id, name: node.title, label: node.title, attributes: [node.type], type: node.type, link_count: node.videos })))
const graphEdges = computed<GraphEdge[]>(() => edges.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, type: edge.id, source_title: nodes.find(node => node.id === edge.source)?.title, target_title: nodes.find(node => node.id === edge.target)?.title })))
const variant = computed(() => variants.find(item => item.key === route.query.variant) || variants[0])
const selectedNode = computed(() => nodes.find(node => node.id === selectedId.value) || nodes[0])
const selectedEdges = computed(() => edges.filter(edge => edge.source === selectedId.value || edge.target === selectedId.value).map(edge => ({ ...edge, otherId: edge.source === selectedId.value ? edge.target : edge.source })))
const pathNodes = computed(() => [...nodes].sort((a, b) => a.x - b.x))
function selectNode(target: string | GraphNode) { selectedId.value = typeof target === 'string' ? target : target.id }
function cycle(delta: number) { const index = variants.findIndex(item => item.key === variant.value.key); const next = variants[(index + delta + variants.length) % variants.length]; void router.replace({ query: { ...route.query, variant: next.key } }) }
function onKeydown(event: KeyboardEvent) { if (['INPUT', 'TEXTAREA', 'SELECT'].includes((event.target as HTMLElement)?.tagName || '') || (event.target as HTMLElement)?.isContentEditable) return; if (event.key === 'ArrowLeft') cycle(-1); if (event.key === 'ArrowRight') cycle(1) }
onMounted(() => window.addEventListener('keydown', onKeydown)); onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<script lang="ts">
import { defineComponent, h, ref as vueRef } from 'vue'
import { AddIcon, MinusIcon, RefreshIcon } from 'tdesign-icons-vue-next'
type TrainingNode = { id: string; title: string; type: string; units: number; videos: number; description: string; goal: string; color: string; x: number; y: number; pathLabel?: string }
type TrainingEdge = { id: string; source: string; target: string; label: string; summary: string; color: string }
const GraphMap = defineComponent({ name: 'GraphMap', props: { nodes: { type: Array, required: true }, edges: { type: Array, required: true }, selectedId: { type: String, required: true }, compact: Boolean }, emits: ['select'], setup(props, { emit }) {
  const scale = vueRef(1); const offset = vueRef({ x: 0, y: 0 }); const dragging = vueRef(false); const dragStart = vueRef({ x: 0, y: 0 }); const origin = vueRef({ x: 0, y: 0 })
  function clamp(value: number) { return Math.max(.65, Math.min(1.8, value)) }
  function zoom(delta: number) { scale.value = clamp(scale.value + delta) }
  function reset() { scale.value = 1; offset.value = { x: 0, y: 0 } }
  function pointerDown(event: PointerEvent) { if ((event.target as HTMLElement).closest('button')) return; dragging.value = true; dragStart.value = { x: event.clientX, y: event.clientY }; origin.value = { ...offset.value }; (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId) }
  function pointerMove(event: PointerEvent) { if (!dragging.value) return; offset.value = { x: origin.value.x + event.clientX - dragStart.value.x, y: origin.value.y + event.clientY - dragStart.value.y } }
  function pointerUp() { dragging.value = false }
  function wheel(event: WheelEvent) { event.preventDefault(); zoom(event.deltaY > 0 ? -.08 : .08) }
  return () => {
    const graphSvg = h('svg', { viewBox: '0 0 100 100', preserveAspectRatio: 'none', 'aria-label': '培训主题关系网络' }, [
      h('defs', [h('marker', { id: 'prototype-arrow', viewBox: '0 0 10 10', refX: '8', refY: '5', markerWidth: '5', markerHeight: '5', orient: 'auto-start-reverse' }, [h('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: 'context-stroke' })])]),
      ...(props.edges as TrainingEdge[]).map(edge => {
        const source = (props.nodes as TrainingNode[]).find(node => node.id === edge.source)!
        const target = (props.nodes as TrainingNode[]).find(node => node.id === edge.target)!
        return h('path', { class: ['graph-map__edge', (props.selectedId === edge.source || props.selectedId === edge.target) && 'is-connected'], d: `M ${source.x} ${source.y} Q ${(source.x + target.x) / 2} ${(source.y + target.y) / 2 - 12} ${target.x} ${target.y}`, stroke: edge.color, 'marker-end': 'url(#prototype-arrow)' })
      }),
    ])
    const graphNodes = (props.nodes as TrainingNode[]).map(node => h('button', { type: 'button', class: ['graph-map__node', props.selectedId === node.id && 'is-selected'], style: { left: `${node.x}%`, top: `${node.y}%`, '--node-color': node.color, '--node-size': `${16 + node.videos * 4}px` }, onClick: () => emit('select', node.id) }, [h('span', { class: 'graph-map__marker' }), h('span', { class: 'graph-map__label' }, [h('strong', node.title)])]))
    const viewport = h('div', { class: 'graph-map__viewport', style: { transform: `translate(${offset.value.x}px, ${offset.value.y}px) scale(${scale.value})` } }, [graphSvg, ...graphNodes])
    const toolbar = h('div', { class: 'graph-map__toolbar', 'aria-label': '图谱缩放控制' }, [h('button', { type: 'button', title: '放大', 'aria-label': '放大', onPointerdown: (event: PointerEvent) => event.stopPropagation(), onClick: () => zoom(.12) }, [h(AddIcon)]), h('button', { type: 'button', title: '缩小', 'aria-label': '缩小', onPointerdown: (event: PointerEvent) => event.stopPropagation(), onClick: () => zoom(-.12) }, [h(MinusIcon)]), h('button', { type: 'button', title: '复位视图', 'aria-label': '复位视图', onPointerdown: (event: PointerEvent) => event.stopPropagation(), onClick: reset }, [h(RefreshIcon)])])
    return h('div', { class: ['graph-map', props.compact && 'graph-map--compact'], onPointerdown: pointerDown, onPointermove: pointerMove, onPointerup: pointerUp, onPointercancel: pointerUp, onWheel: wheel }, [toolbar, viewport])
  }
} })
export default { components: { GraphMap } }
</script>

<style>
.training-graph-prototype { display: grid; gap: 14px; min-width: 0; }
.training-prototype__notice { display: grid; gap: 4px; padding: 12px 16px; border: 1px solid rgba(255,255,255,.8); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.46); color: var(--td-text-color-secondary); }
.training-prototype__notice strong { color: var(--td-text-color-primary); font-size: 16px; }.training-prototype__notice span:last-child { font-size: 12px; }.training-prototype__eyebrow,.surface-kicker { color: var(--td-brand-color); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.prototype-surface { display: grid; gap: 14px; min-width: 0; padding: 18px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-large); background: rgba(255,255,255,.34); box-shadow: 0 16px 42px rgba(58,76,68,.07); backdrop-filter: blur(18px) saturate(120%); }
.prototype-surface__head { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }.prototype-surface__head h2 { margin: 2px 0 0; font-size: 21px; }.prototype-surface__head p { margin: 4px 0 0; color: var(--td-text-color-secondary); font-size: 12px; }.surface-status { color: var(--td-brand-color); font-size: 12px; }.surface-metrics { display: flex; align-items: baseline; gap: 6px; color: var(--td-text-color-secondary); font-size: 11px; }.surface-metrics strong { margin-left: 8px; color: var(--td-text-color-primary); font-size: 18px; }
.graph-map { position: relative; min-height: 560px; overflow: hidden; border: 1px solid rgba(255,255,255,.8); border-radius: var(--td-radius-medium); background: radial-gradient(circle, rgba(52,79,65,.12) 1px, transparent 1.5px), rgba(255,255,255,.22); background-size: 26px 26px; cursor: grab; }.graph-map:active { cursor: grabbing; }.graph-map--compact { min-height: 520px; }.graph-map__viewport { position: absolute; inset: 0; transition: transform .16s ease-out; }.graph-map svg { position: absolute; inset: 0; width: 100%; height: 100%; }.graph-map__toolbar { position: absolute; z-index: 3; top: 12px; left: 12px; display: inline-flex; gap: 4px; padding: 4px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-small); background: rgba(255,255,255,.68); box-shadow: 0 6px 16px rgba(52,79,65,.1); }.graph-map__toolbar button { width: 28px; height: 28px; border: 0; border-radius: 5px; background: transparent; color: var(--td-text-color-primary); font-size: 17px; line-height: 1; cursor: pointer; }.graph-map__toolbar button:hover { background: rgba(52,79,65,.1); }.graph-map__edge { fill: none; stroke-width: .42; opacity: .56; transition: opacity .2s, stroke-width .2s; }.graph-map__edge.is-connected { stroke-width: .75; opacity: .95; }.graph-map__node { position: absolute; display: grid; grid-template-columns: var(--node-size) minmax(0, 1fr); align-items: center; gap: 8px; max-width: 210px; padding: 5px 8px 5px 5px; transform: translate(calc(var(--node-size) / -2), -50%); border: 1px solid transparent; border-radius: 10px; background: rgba(255,255,255,.72); color: inherit; text-align: left; cursor: pointer; }.graph-map__node:hover,.graph-map__node.is-selected { border-color: var(--td-brand-color); background: rgba(255,255,255,.94); box-shadow: 0 7px 18px rgba(52,79,65,.12); }.graph-map__marker,.node-dot,.path-node__dot { width: var(--node-size, 22px); height: var(--node-size, 22px); flex: 0 0 auto; border: 4px solid color-mix(in srgb, var(--node-color) 34%, white); border-radius: 50%; background: var(--node-color); box-shadow: 0 0 0 3px rgba(255,255,255,.82); }.graph-map__label { display: grid; gap: 2px; min-width: 0; }.graph-map__label strong { font-size: 12px; line-height: 17px; white-space: normal; }.graph-map__label small { color: var(--td-text-color-secondary); font-size: 10px; white-space: nowrap; }
.inspector-layout { display: grid; grid-template-columns: minmax(0, 1.35fr) minmax(260px, .65fr); gap: 14px; }.prototype-inspector { display: grid; align-content: start; gap: 14px; padding: 18px; border-left: 1px solid rgba(255,255,255,.72); }.inspector-node { display: flex; align-items: center; gap: 10px; }.inspector-node strong,.inspector-node small { display: block; }.inspector-node small,.prototype-inspector p { color: var(--td-text-color-secondary); font-size: 12px; line-height: 18px; }.prototype-inspector p { margin: 0; }.inspector-section { display: grid; gap: 8px; padding-top: 12px; border-top: 1px solid rgba(94,115,102,.16); }.inspector-section > span { color: var(--td-text-color-secondary); font-size: 11px; }.inspector-section > strong { font-size: 13px; line-height: 19px; }.relation-row { display: grid; gap: 2px; padding: 8px 10px; border: 1px solid rgba(255,255,255,.72); border-radius: var(--td-radius-small); background: rgba(255,255,255,.42); color: inherit; text-align: left; cursor: pointer; }.relation-row b { color: var(--td-brand-color); font-size: 11px; }.relation-row span { color: var(--td-text-color-secondary); font-size: 11px; }
.path-legend { display: flex; flex-wrap: wrap; gap: 10px; color: var(--td-text-color-secondary); font-size: 11px; }.path-legend span { display: inline-flex; align-items: center; gap: 5px; }.path-legend i { width: 7px; height: 7px; border-radius: 50%; }.learning-path { display: grid; grid-template-columns: repeat(6, minmax(130px, 1fr)); gap: 0; overflow-x: auto; padding: 44px 6px 30px; }.path-column { position: relative; min-width: 130px; }.path-connector { position: absolute; top: -26px; left: -1px; width: calc(100% + 2px); border-top: 1px solid rgba(71,98,83,.34); color: var(--td-text-color-secondary); text-align: center; font-size: 10px; }.path-connector span { position: relative; top: -19px; padding: 0 5px; background: rgba(244,247,245,.9); }.path-node { display: grid; gap: 7px; width: calc(100% - 10px); min-height: 214px; padding: 14px 12px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.58); color: inherit; text-align: left; cursor: pointer; }.path-node:hover,.path-node.is-selected { border-color: var(--td-brand-color); background: rgba(255,255,255,.9); transform: translateY(-3px); }.path-node__index { color: var(--td-text-color-secondary); font-size: 10px; }.path-node strong { font-size: 13px; line-height: 18px; }.path-node small { color: var(--td-text-color-secondary); font-size: 10px; }.path-node em { color: var(--td-text-color-secondary); font-size: 11px; font-style: normal; line-height: 17px; }
.prototype-switcher { position: sticky; bottom: 14px; z-index: 5; justify-self: center; display: inline-flex; align-items: center; gap: 12px; padding: 7px 10px; border: 1px solid rgba(255,255,255,.9); border-radius: 999px; background: rgba(39,59,49,.94); box-shadow: 0 12px 28px rgba(36,52,43,.24); color: #fff; }.prototype-switcher button { display: grid; width: 30px; height: 30px; place-items: center; border: 0; border-radius: 50%; background: rgba(255,255,255,.12); color: #fff; cursor: pointer; }.prototype-switcher button:hover { background: rgba(255,255,255,.24); }.prototype-switcher span { display: grid; grid-template-columns: max-content max-content; align-items: baseline; gap: 8px; min-width: 150px; }.prototype-switcher b { color: #9cc9aa; font-size: 10px; }.prototype-switcher strong { font-size: 12px; }
@media (max-width: 820px) { .prototype-surface__head { align-items: flex-start; flex-direction: column; gap: 8px; }.inspector-layout { grid-template-columns: 1fr; }.prototype-inspector { border-top: 1px solid rgba(255,255,255,.72); border-left: 0; }.graph-map { min-height: 500px; }.graph-map__node { max-width: 170px; }.learning-path { grid-template-columns: repeat(6, 145px); }.prototype-switcher { bottom: 8px; } }
</style>
