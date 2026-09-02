<template>
  <main class="knowledge-graph">
    <header class="knowledge-graph__header">
      <div><h1>Knowledge Graph</h1><p>探索视频知识之间的连接</p></div>
      <span v-if="payload" class="knowledge-graph__count">{{ filteredNodes.length }} 个节点 · {{ filteredEdges.length }} 条正式关系 · {{ filteredReadingAssociations.length }} 条阅读关联</span>
    </header>
    <div v-if="loading" class="knowledge-graph__state"><t-loading text="正在加载知识图谱" /></div>
    <t-alert v-else-if="error" theme="error" :message="error" />
    <t-empty v-else-if="!payload || !payload.nodes.length" description="暂无知识图谱" />
    <template v-else>
      <AttributeFilterTabs v-model="selectedAttribute" :attributes="payload.attributes" :counts="attributeCounts" :total="payload.nodes.length" />
      <section class="knowledge-graph__canvas-wrap">
        <div v-if="payload.meta.truncated" class="knowledge-graph__hint">显示 {{ payload.meta.returned }} / {{ payload.meta.total }} 节点</div>
        <GraphCanvas :nodes="filteredNodes" :edges="filteredEdges" :reading-associations="filteredReadingAssociations" @node-click="selectedNodeId = $event.id" />
      </section>
      <section v-if="wikiPages.length" class="knowledge-graph__wiki-list" aria-labelledby="knowledge-graph-wiki-list-title">
        <header>
          <div>
            <h2 id="knowledge-graph-wiki-list-title">知识 Wiki 页面</h2>
            <p>来自 extract-video-knowledge 的独立知识页</p>
          </div>
          <span>{{ wikiPages.length }} 页</span>
        </header>
        <div class="knowledge-graph__wiki-grid">
          <button v-for="page in wikiPages" :key="page.id" type="button" :class="{ 'is-selected': selectedNode?.knowledge_detail?.id === page.id }" @click="selectWikiPage(page.id)">
            <span class="knowledge-graph__wiki-title">{{ page.title }}</span>
            <span class="knowledge-graph__wiki-meta">{{ pageTypeLabel(page) }} · {{ page.source_video_title || page.video_title || '未知来源' }} · {{ page.structure_fields?.length || 0 }} 个结构字段</span>
            <small v-if="!nodeIdByWikiPageId[page.id]">未入图</small>
          </button>
        </div>
      </section>
      <NodeDetailPanel
        v-if="selectedNode"
        :key="selectedNode.id"
        :node="selectedNode"
        :related-nodes="relatedNodes"
        :related-edges="relatedEdges"
        :reading-associations="relatedReadingAssociations"
        @close="selectedNodeId = null"
        @select-video-by-id="openVideo"
        @open-wiki-page="openWikiPage"
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
import type { GraphKnowledgeDetail, GraphNode, KnowledgeGraphPayload } from '@/types/videohub'

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
const filteredReadingAssociations = computed(() => payload.value?.reading_associations?.filter(edge => filteredNodeIds.value.has(edge.source) && edge.target_exists && filteredNodeIds.value.has(edge.target)) ?? [])
const selectedNode = computed(() => {
  const existing = payload.value?.nodes.find(node => node.id === selectedNodeId.value)
  if (existing) return existing
  if (!selectedNodeId.value?.startsWith('wiki:')) return null
  const page = wikiPages.value.find(item => `wiki:${item.id}` === selectedNodeId.value)
  return page ? wikiPageNode(page) : null
})
const relatedEdges = computed(() => payload.value?.edges.filter(edge => edge.source === selectedNodeId.value || edge.target === selectedNodeId.value) ?? [])
const relatedReadingAssociations = computed(() => payload.value?.reading_associations?.filter(edge => edge.source === selectedNodeId.value || edge.target === selectedNodeId.value) ?? [])
const relatedNodeIds = computed(() => new Set(relatedEdges.value.flatMap(edge => [edge.source, edge.target]).filter(id => id !== selectedNodeId.value)))
const relatedNodes = computed(() => payload.value?.nodes.filter(node => relatedNodeIds.value.has(node.id)) ?? [])
const wikiPages = computed(() => payload.value?.wiki_pages ?? [])
const nodeIdByWikiPageId = computed(() => Object.fromEntries((payload.value?.nodes ?? []).flatMap(node => node.knowledge_detail?.id ? [[node.knowledge_detail.id, node.id]] : [])))

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
function openWikiPage(slug: string) {
  const kbId = payload.value?.knowledge_base_id
  if (!kbId || !slug) return
  // The native Wiki graph view owns the reading drawer. Keeping the page
  // address keyed by KB id + slug lets WikiBrowser fetch the exact page and
  // open that drawer without duplicating reader logic here.
  const href = router.resolve({ name: 'knowledgeBaseDetail', params: { kbId }, query: { tab: 'graph', slug } }).href
  window.open(href, '_blank', 'noopener,noreferrer')
}
function selectWikiPage(pageId: string) {
  selectedNodeId.value = nodeIdByWikiPageId.value[pageId] ?? `wiki:${pageId}`
}
function wikiPageNode(page: GraphKnowledgeDetail): GraphNode {
  const typeLabel = pageTypeLabel(page)
  return {
    id: `wiki:${page.id}`,
    name: page.title,
    label: page.title,
    attributes: [typeLabel === '人物' || typeLabel === '机构' || typeLabel === '产品' || typeLabel === '技术' || typeLabel === '行业' || typeLabel === '地点' ? '实体' : typeLabel],
    type: typeLabel === '人物' || typeLabel === '机构' || typeLabel === '产品' || typeLabel === '技术' || typeLabel === '行业' || typeLabel === '地点' ? '实体' : typeLabel,
    video_id: page.video_id,
    video_title: page.source_video_title || page.video_title,
    seconds: page.seconds || 0,
    link_count: 0,
    knowledge_detail: page,
  }
}
function pageTypeLabel(page: GraphKnowledgeDetail) {
  const labels: Record<string, string> = { entity: '实体', concept: '概念', case: '案例', methodology: '方法论', insight: '洞察' }
  const entityLabels: Record<string, string> = { person: '人物', organization: '机构', product: '产品', technology: '技术', industry: '行业', place: '地点' }
  return page.entity_sub_type ? entityLabels[page.entity_sub_type] || '实体' : labels[page.knowledge_type] || page.knowledge_type
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
.knowledge-graph__wiki-list { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); min-width: 0; padding-top: calc(var(--td-comp-margin-s) * 2); border-top: 1px solid var(--td-component-stroke); }
.knowledge-graph__wiki-list header { display: flex; align-items: flex-end; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 2); }
.knowledge-graph__wiki-list h2 { margin: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-medium); }
.knowledge-graph__wiki-list p { margin: calc(var(--td-comp-margin-s) / 2) 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.knowledge-graph__wiki-list header > span { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); white-space: nowrap; }
.knowledge-graph__wiki-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: var(--td-comp-margin-s); }
.knowledge-graph__wiki-grid button { display: grid; gap: 2px; min-width: 0; padding: var(--td-comp-margin-s); border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--td-radius-medium); background: var(--td-bg-color-container); text-align: left; cursor: pointer; }
.knowledge-graph__wiki-grid button:hover, .knowledge-graph__wiki-grid button.is-selected { border-color: var(--td-brand-color); background: var(--td-bg-color-secondarycontainer); }
.knowledge-graph__wiki-title { overflow: hidden; color: var(--td-text-color-primary); text-overflow: ellipsis; white-space: nowrap; }
.knowledge-graph__wiki-meta { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.knowledge-graph__wiki-grid small { color: var(--td-warning-color); font-size: var(--td-font-size-body-small); }
@media (max-width: 720px) { .knowledge-graph__header { align-items: flex-start; flex-direction: column; } }
</style>
