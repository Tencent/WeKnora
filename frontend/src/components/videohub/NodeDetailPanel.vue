<template>
  <teleport to="body">
    <div class="node-panel-layer" @keydown.esc="emit('close')">
      <button class="node-panel-mask" type="button" aria-label="关闭节点详情" @click="emit('close')" />
      <aside class="node-panel" role="dialog" aria-modal="true" aria-labelledby="node-panel-title" tabindex="-1">
        <header>
          <div>
            <span class="node-panel__tag" :style="{ color: attributeColor, borderColor: attributeColor }">{{ detailTypeLabel }}</span>
            <h2 id="node-panel-title">{{ panelTitle }}</h2>
          </div>
          <t-button variant="text" shape="square" aria-label="关闭" @click="emit('close')"><t-icon name="close" /></t-button>
        </header>
        <p v-if="node.is_orphan" class="node-panel__orphan">孤岛知识：暂无正式语义关系</p>
        <section>
          <h3>{{ isEntityNode ? '一句话概述' : '核心内容' }}</h3>
          <p v-if="detail?.core_content" class="node-panel__summary">{{ detail.core_content }}</p>
          <p v-else class="node-panel__muted">当前 Wiki 页面缺少{{ isEntityNode ? '一句话概述' : '核心内容' }}</p>
        </section>
        <section v-if="detail?.structure_fields?.length">
          <h3>{{ isEntityNode ? '关键信息维度' : '结构维度' }}</h3>
          <dl class="node-panel__fields">
            <template v-for="field in detail.structure_fields" :key="field.key">
              <dt>{{ field.label }}</dt>
              <dd>{{ field.value }}</dd>
            </template>
          </dl>
        </section>
        <section v-else>
          <h3>{{ isEntityNode ? '关键信息维度' : '结构维度' }}</h3>
          <p class="node-panel__muted">当前 Wiki 页面缺少按 {{ typeFrameworkLabel }} 提取的结构字段</p>
        </section>
        <section>
          <h3>知识来源</h3>
          <dl class="node-panel__fields">
            <dt>视频名称</dt>
            <dd>{{ sourceVideoTitle }}</dd>
            <dt>定位时间戳</dt>
            <dd><button v-if="sourceVideoID" class="node-panel__time" type="button" @click="selectVideo(sourceVideoID, sourceSeconds)">{{ sourceTimestamp }}</button><span v-else>{{ sourceTimestamp }}</span></dd>
          </dl>
        </section>
        <section>
          <h3>信息性质</h3>
          <span class="node-panel__tag" :style="{ color: attributeColor, borderColor: attributeColor }">{{ detail?.information_nature || detailTypeLabel }}</span>
        </section>
        <section>
          <h3>相关内容</h3>
          <ul v-if="relatedContentLinks.length" class="node-panel__links">
            <li v-for="link in relatedContentLinks" :key="link.key">
              <button v-if="link.slug" class="node-panel__wiki-link" type="button" @click="openWikiPage(link.slug)">{{ link.title }}</button>
              <span v-else>{{ link.title }}</span>
              <small v-if="link.relation">{{ getRelationTypeLabel(link.relation) }}</small>
            </li>
          </ul>
          <p v-else class="node-panel__muted">暂无相关内容</p>
        </section>
        <section>
          <h3>结构化关系</h3>
          <ul v-if="structuredRelations.length" class="node-panel__links">
            <li v-for="relation in structuredRelations" :key="relation.key">
              <button v-if="relation.targetSlug" class="node-panel__wiki-link" type="button" @click="openWikiPage(relation.targetSlug)">{{ relation.targetTitle }}</button>
              <span v-else>{{ relation.targetTitle }}</span>
              <small>{{ getRelationTypeLabel(relation.relationType) }}<span v-if="relation.confidence"> / {{ Math.round(relation.confidence * 100) }}%</span></small>
            </li>
          </ul>
          <p v-else class="node-panel__muted">暂无已验证的结构化关系</p>
        </section>
      </aside>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted } from 'vue'
import { FALLBACK_ATTRIBUTE_COLOR, KNOWN_ATTRIBUTES } from './graphStyles'
import { getRelationTypeLabel } from './knowledgeTypeStyles'
import type { GraphEdge, GraphNode, GraphReadingAssociation, WikiDetailLink } from '@/types/videohub'

const props = defineProps<{ node: GraphNode; relatedNodes: GraphNode[]; relatedEdges: GraphEdge[]; readingAssociations: GraphReadingAssociation[] }>()
const emit = defineEmits<{ close: []; selectVideoById: [videoId: string, seconds: number]; openWikiPage: [slug: string] }>()
const attributeColor = computed(() => `var(${KNOWN_ATTRIBUTES[props.node.attributes[0]] ?? FALLBACK_ATTRIBUTE_COLOR})`)
const detail = computed(() => props.node.knowledge_detail)
const panelTitle = computed(() => detail.value?.title || props.node.label)
const isEntityNode = computed(() => props.node.type === '实体' || props.node.attributes.includes('实体') || detail.value?.knowledge_type === 'entity')
const detailTypeLabel = computed(() => {
  if (detail.value?.entity_sub_type) return entitySubTypeLabels[detail.value.entity_sub_type] ?? '实体'
  if (detail.value?.knowledge_type) return knowledgeTypeLabels[detail.value.knowledge_type] ?? props.node.attributes[0] ?? '无分类'
  return props.node.attributes[0] || '无分类'
})
const typeFrameworkLabel = computed(() => detailTypeLabel.value || '知识类型')
const knowledgeTypeLabels: Record<string, string> = { entity: '实体', concept: '概念', case: '案例', methodology: '方法论', insight: '洞察' }
const entitySubTypeLabels: Record<string, string> = { person: '人物', organization: '机构', product: '产品', technology: '技术', industry: '行业', place: '地点' }
const firstEvidence = computed(() => props.node.evidence?.[0])
const sourceVideoID = computed(() => detail.value?.video_id || firstEvidence.value?.video_id || props.node.video_id || '')
const sourceVideoTitle = computed(() => detail.value?.source_video_title || detail.value?.video_title || firstEvidence.value?.video_title || props.node.video_title || '未命名视频')
const sourceSeconds = computed(() => detail.value?.seconds ?? (firstEvidence.value ? firstEvidence.value.start_ms / 1000 : props.node.seconds || 0))
const sourceTimestamp = computed(() => detail.value?.timestamp || detail.value?.time_range || (firstEvidence.value ? formatRange(firstEvidence.value.start_ms, firstEvidence.value.end_ms) : formatTime(sourceSeconds.value)))
const relatedContentLinks = computed(() => mergeRelatedLinks(detail.value?.related_content || []))
const structuredRelations = computed(() => (detail.value?.relations || []).map((relation, index) => {
  const target = props.relatedNodes.find(node =>
    (relation.target_wiki_page_id && node.wiki_page_id === relation.target_wiki_page_id) ||
    (relation.target_object_id && node.knowledge_object_id === relation.target_object_id),
  )
  return {
    key: relation.relation_id || `${relation.target_wiki_page_id || relation.target_object_id || 'target'}-${index}`,
    targetTitle: relation.target_title || target?.knowledge_detail?.title || target?.label || relation.target_wiki_page_id || relation.target_object_id || '未知对象',
    targetSlug: relation.target_slug || target?.knowledge_detail?.slug,
    relationType: relation.relation_type,
    confidence: relation.confidence,
  }
}))
function formatTime(seconds = 0) { const value = Math.max(0, Math.floor(seconds)); return `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}` }
function formatRange(startMs: number, endMs: number) { return `${formatTime(startMs / 1000)} - ${formatTime(endMs / 1000)}` }
function selectVideo(videoId: string, seconds = 0) { emit('selectVideoById', videoId, seconds) }
function openWikiPage(slug: string) { emit('openWikiPage', slug) }
function edgeForNode(nodeId: string) { return props.relatedEdges.find(edge => edge.source === nodeId || edge.target === nodeId) }
function mergeRelatedLinks(explicitLinks: WikiDetailLink[]) {
  const out: Array<{ key: string; title: string; slug?: string; relation?: string }> = []
  const seen = new Set<string>()
  const add = (title: string, slug?: string, relation?: string) => {
    title = title.trim()
    slug = slug?.trim() || undefined
    if (!title) return
    const key = `${slug || ''}\u0000${title}`.toLowerCase()
    if (seen.has(key)) return
    seen.add(key)
    out.push({ key, title, slug, relation })
  }
  explicitLinks.forEach(link => add(link.title, link.slug))
  props.relatedNodes.forEach(node => {
    const edge = edgeForNode(node.id)
    add(node.knowledge_detail?.title || node.label || node.name, node.knowledge_detail?.slug, edge?.type)
  })
  props.readingAssociations.filter(item => item.target_exists).forEach(association => {
    const target = props.relatedNodes.find(node => node.id === association.target)
    if (target) add(target.knowledge_detail?.title || target.label, target.knowledge_detail?.slug)
  })
  return out
}
onMounted(() => nextTick(() => document.querySelector<HTMLElement>('.node-panel')?.focus()))
</script>

<style scoped>
.node-panel-layer { position: fixed; z-index: 3000; inset: 0; }
.node-panel-mask { position: absolute; inset: 0; width: 100%; border: 0; background: color-mix(in srgb, var(--td-text-color-primary) 40%, transparent); backdrop-filter: blur(2px); cursor: default; }
.node-panel { position: absolute; top: 0; right: 0; bottom: 0; overflow-y: auto; width: min(360px, 92vw); padding: calc(var(--td-comp-margin-s) * 2.5) calc(var(--td-comp-margin-s) * 3); border: var(--border-width-hairline, .5px) solid var(--color-stroke, var(--td-component-stroke)); border-right: 0; border-radius: var(--rounded-popup, 10px) 0 0 var(--rounded-popup, 10px); outline: none; background: var(--color-bg-popup, var(--td-bg-color-container)); box-shadow: var(--shadow-popup); backdrop-filter: blur(20px) saturate(180%); animation: panel-in .18s cubic-bezier(.2, 0, 0, 1); }
.node-panel header { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--td-comp-margin-s); }
.node-panel h2 { margin: var(--td-comp-margin-s) 0 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-medium); }
.node-panel h3 { margin: 0 0 var(--td-comp-margin-s); color: var(--td-text-color-primary); font-size: var(--td-font-size-title-small); }
.node-panel section { padding: calc(var(--td-comp-margin-s) * 2) 0; border-top: 1px solid var(--td-component-stroke); }
.node-panel__meta-line { margin: calc(var(--td-comp-margin-s) * 1.5) 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.node-panel__orphan { margin: var(--td-comp-margin-s) 0 0; color: var(--td-warning-color); font-size: var(--td-font-size-body-small); }
.node-panel section p, .node-panel__summary { color: var(--td-text-color-secondary); line-height: 1.65; }
.node-panel__tag { display: inline-flex; padding: calc(var(--td-comp-margin-s) / 4) var(--td-comp-margin-s); border: var(--border-width-hairline, .5px) solid; border-radius: var(--td-radius-round); background: var(--td-bg-color-secondarycontainer); font-size: var(--td-font-size-body-small); }
.node-panel ul { margin: 0; padding-left: calc(var(--td-comp-margin-s) * 2); color: var(--td-text-color-secondary); }
.node-panel__fields { display: grid; grid-template-columns: minmax(76px, max-content) minmax(0, 1fr); gap: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); margin: 0; padding: var(--td-comp-margin-s); border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); }
.node-panel__fields dt { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); white-space: nowrap; }
.node-panel__fields dd { min-width: 0; margin: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-small); line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere; }
.node-panel__evidence-tags { display: flex; flex-wrap: wrap; gap: calc(var(--td-comp-margin-s) / 2); }
.node-panel__source-ranges { display: flex; flex-wrap: wrap; gap: var(--td-comp-margin-s); margin-bottom: var(--td-comp-margin-s); }
.node-panel__source { display: inline-flex; align-items: center; min-height: 28px; padding: 0 var(--td-comp-margin-s); border: 1px solid color-mix(in srgb, var(--td-brand-color) 28%, var(--td-component-stroke)); border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-brand-color) 6%, transparent); color: var(--td-brand-color); font: inherit; cursor: pointer; }
.node-panel__time { padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; cursor: pointer; }
.node-panel__wiki-link { min-width: 0; padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; text-align: left; cursor: pointer; overflow-wrap: anywhere; }
.node-panel__wiki-link:hover { text-decoration: underline; }
.node-panel__links { display: grid; gap: var(--td-comp-margin-s); margin: 0; padding: 0 !important; list-style: none; color: var(--td-text-color-primary); }
.node-panel__links li { display: grid; gap: 2px; min-width: 0; }
.node-panel__links span { min-width: 0; overflow-wrap: anywhere; }
.node-panel__links small { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.node-panel__evidence { display: grid; gap: var(--td-comp-margin-s); padding: 0 !important; list-style: none; }
.node-panel__evidence li { display: flex; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); }
.node-panel__evidence span { overflow: hidden; color: var(--td-text-color-primary); text-overflow: ellipsis; white-space: nowrap; }
.node-panel__muted { color: var(--td-text-color-placeholder) !important; }
@keyframes panel-in { from { transform: translateX(20px); opacity: 0; } }
@media (max-width: 520px) { .node-panel__fields { grid-template-columns: 1fr; } }
</style>
