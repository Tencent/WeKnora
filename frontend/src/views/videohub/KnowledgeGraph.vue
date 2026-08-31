<template>
  <main class="knowledge-graph">
    <header class="knowledge-graph__header">
      <div><h1>Knowledge Graph</h1><p>探索视频知识之间的连接</p></div>
      <span v-if="payload" class="knowledge-graph__count">{{ filteredNodes.length }} 个节点 · {{ filteredEdges.length }} 条关系</span>
    </header>
    <div v-if="loading" class="knowledge-graph__state"><t-loading text="正在加载知识图谱" /></div>
    <t-alert v-else-if="error" theme="error" :message="error" />
    <t-empty v-else-if="!payload || !payload.nodes.length" description="暂无知识图谱" />
    <template v-else>
      <AttributeFilterTabs v-model="selectedAttribute" :attributes="payload.attributes" :counts="attributeCounts" :total="payload.nodes.length" />
      <section class="knowledge-graph__canvas-wrap">
        <div v-if="payload.meta.truncated" class="knowledge-graph__hint">显示 {{ payload.meta.returned }} / {{ payload.meta.total }} 节点</div>
        <GraphCanvas :nodes="filteredNodes" :edges="filteredEdges" @node-click="selectedNodeId = $event.id" />
      </section>
      <NodeDetailPanel
        v-if="selectedNode"
        :key="selectedNode.id"
        :node="selectedNode"
        :related-nodes="relatedNodes"
        :related-edges="relatedEdges"
        @close="selectedNodeId = null"
        @select-video-by-id="openVideo"
      />
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { fetchKnowledgeGraph } from '@/api/videohub/knowledgeGraph'
import AttributeFilterTabs from '@/components/videohub/AttributeFilterTabs.vue'
import GraphCanvas from '@/components/videohub/GraphCanvas.vue'
import NodeDetailPanel from '@/components/videohub/NodeDetailPanel.vue'
import type { KnowledgeGraphPayload } from '@/types/videohub'

const router = useRouter()
const route = useRoute()
const loading = ref(true)
const error = ref('')
const payload = ref<KnowledgeGraphPayload | null>(null)
const selectedAttribute = ref('all')
const selectedNodeId = ref<string | null>(null)

const attributeCounts = computed(() => Object.fromEntries((payload.value?.attributes ?? []).map(attribute => [attribute, payload.value?.nodes.filter(node => node.attributes[0] === attribute).length ?? 0])))
const filteredNodes = computed(() => payload.value?.nodes.filter(node => selectedAttribute.value === 'all' || node.attributes[0] === selectedAttribute.value) ?? [])
const filteredNodeIds = computed(() => new Set(filteredNodes.value.map(node => node.id)))
const filteredEdges = computed(() => payload.value?.edges.filter(edge => filteredNodeIds.value.has(edge.source) && filteredNodeIds.value.has(edge.target)) ?? [])
const selectedNode = computed(() => payload.value?.nodes.find(node => node.id === selectedNodeId.value) ?? null)
const relatedEdges = computed(() => payload.value?.edges.filter(edge => edge.source === selectedNodeId.value || edge.target === selectedNodeId.value) ?? [])
const relatedNodeIds = computed(() => new Set(relatedEdges.value.flatMap(edge => [edge.source, edge.target]).filter(id => id !== selectedNodeId.value)))
const relatedNodes = computed(() => payload.value?.nodes.filter(node => relatedNodeIds.value.has(node.id)) ?? [])

async function load() {
  loading.value = true
  const requestedLimit = Number(route.query.limit)
  const limit = Number.isFinite(requestedLimit) && requestedLimit > 0 ? Math.floor(requestedLimit) : 500
  error.value = ''
  try { payload.value = await fetchKnowledgeGraph({ mode: 'overview', limit }) }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '知识图谱加载失败' }
  finally { loading.value = false }
}
function openVideo(videoId: string, seconds: number) {
  const href = router.resolve({ name: 'videoDetail', params: { videoId }, query: { t: Math.max(0, Math.floor(seconds)) } }).href
  window.open(href, '_blank', 'noopener,noreferrer')
}
watch(selectedAttribute, () => { selectedNodeId.value = null })
onMounted(load)
</script>

<style scoped>
.knowledge-graph { display: grid; align-content: start; gap: calc(var(--td-comp-margin-s) * 2); min-height: 100%; padding: calc(var(--td-comp-margin-s) * 2); background: var(--td-bg-color-container); }
.knowledge-graph__header { display: flex; align-items: flex-end; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 2); }
.knowledge-graph h1 { margin: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-headline-medium); }
.knowledge-graph__header p { margin: calc(var(--td-comp-margin-s) / 2) 0 0; color: var(--td-text-color-secondary); }
.knowledge-graph__count { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.knowledge-graph__state, .knowledge-graph > :deep(.t-empty) { min-height: 420px; display: grid; place-items: center; }
.knowledge-graph__canvas-wrap { position: relative; min-width: 0; }
.knowledge-graph__hint { position: absolute; z-index: 2; top: var(--td-comp-margin-s); left: 50%; padding: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); transform: translateX(-50%); border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--rounded-popup, 10px); background: var(--td-bg-color-container); box-shadow: var(--td-shadow-2); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
@media (max-width: 720px) { .knowledge-graph__header { align-items: flex-start; flex-direction: column; } }
</style>
