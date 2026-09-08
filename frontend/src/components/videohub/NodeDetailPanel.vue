<template>
  <teleport to="body">
    <div class="node-panel-layer" @keydown.esc="emit('close')">
      <button class="node-panel-mask" type="button" aria-label="关闭图谱节点详情" @click="emit('close')" />
      <aside class="node-panel" role="dialog" aria-modal="true" aria-labelledby="node-panel-title" tabindex="-1">
        <header class="node-panel__header">
          <div>
            <span class="node-panel__tag" :style="{ color: attributeColor, borderColor: attributeColor }">{{ typeLabel }}</span>
            <h2 id="node-panel-title">{{ title }}</h2>
          </div>
          <t-button variant="text" shape="square" aria-label="关闭" @click="emit('close')"><t-icon name="close" /></t-button>
        </header>

        <div v-if="detailLoading" class="node-panel__state"><t-loading size="small" /> 正在读取知识详情</div>
        <div v-else-if="detailError" class="node-panel__failure"><t-alert theme="error" :message="detailError" /><t-button size="small" variant="text" @click="emit('retryDetail')">重试</t-button></div>

        <article v-if="detailLoaded && detail" class="node-panel__article">
          <div v-if="detailStatus !== 'ready'" class="node-panel__notice">{{ detailStatusLabel }}</div>
          <p class="node-panel__lead">{{ detail.core_content || `当前 Wiki 页面缺少${isEntity ? '一句话概述' : '核心内容'}` }}</p>

          <div class="node-panel__meta">
            <button v-if="sourceVideoId && firstEvidence" type="button" class="node-panel__time" @click="selectVideo(sourceVideoId, firstEvidence.start_ms / 1000)"><t-icon name="time" />{{ formatRange(firstEvidence.start_ms, firstEvidence.end_ms) }}</button>
            <span><t-icon name="video" />{{ sourceVideoTitle || '来源视频不可读' }}</span>
            <span v-if="detail.classification_confidence"><t-icon name="check-circle" />可信度 {{ Math.round(detail.classification_confidence * 100) }}%</span>
          </div>

          <section v-if="detail.structure_fields?.length" class="node-panel__section">
            <div v-for="field in detail.structure_fields" :key="field.key" class="node-panel__paragraph">
              <h3>{{ field.label }}</h3>
              <p>{{ field.value }}</p>
            </div>
          </section>

          <section class="node-panel__section node-panel__relations">
            <h3>知识关系 <small>{{ formalLinks.length + readingLinks.length }}</small></h3>
            <div class="node-panel__relation-group">
              <h4>正式关系 <small>{{ formalLinks.length }}</small></h4>
              <ul v-if="formalLinks.length" class="node-panel__links">
                <li v-for="link in formalLinks" :key="link.key">
                  <button v-if="link.targetPageId" type="button" :aria-label="`在图谱中查看${link.title}`" @click="selectGraphNode(link)">{{ link.description }}</button>
                  <span v-else>{{ link.description }}</span>
                  <small>{{ link.type }}<template v-if="link.confidence"> · 可信度 {{ Math.round(link.confidence * 100) }}%</template></small>
                </li>
              </ul>
              <p v-else class="node-panel__muted">暂无已验证的正式关系</p>
            </div>

            <div class="node-panel__relation-group">
              <h4>阅读关联 <small>{{ readingLinks.length }}</small></h4>
              <ul v-if="readingLinks.length" class="node-panel__links">
                <li v-for="link in readingLinks" :key="link.key">
                  <button v-if="link.targetPageId" type="button" :aria-label="`在图谱中查看${link.title}`" @click="selectGraphNode(link)">{{ link.description }}</button>
                  <span v-else>{{ link.description }}</span>
                </li>
              </ul>
              <p v-else class="node-panel__muted">暂无阅读关联</p>
            </div>
          </section>

          <section v-if="evidenceCount" class="node-panel__section">
            <h3>原文证据 <small>{{ evidenceCount }}</small></h3>
            <blockquote v-for="item in evidence" :key="item.evidence_sentence_id || `${item.start_ms}-${item.end_ms}`">
              <p>“{{ item.text || '暂无证据原文' }}”</p>
              <button type="button" class="node-panel__time" @click="selectVideo(item.video_id || sourceVideoId, item.start_ms / 1000)">{{ formatRange(item.start_ms, item.end_ms) }}</button>
            </blockquote>
          </section>

          <section v-if="crossVideoLoading || crossVideoStatus !== 'idle'" class="node-panel__section">
            <h3>跨视频来源</h3>
            <div v-if="crossVideoLoading" class="node-panel__state"><t-loading size="small" /> 正在读取跨视频关联</div>
            <div v-else-if="crossVideoStatus === 'failed'" class="node-panel__failure"><span>{{ crossVideoError || '跨视频关联读取失败' }}</span><t-button size="small" variant="text" @click="emit('retryCrossVideo')">重试</t-button></div>
            <ul v-else-if="crossVideoAssociations.length" class="node-panel__links"><li v-for="association in crossVideoAssociations" :key="association.id"><span>{{ association.target_video_title }}</span><small>{{ association.relation_description }}</small><button type="button" class="node-panel__time" @click="selectVideo(association.target_evidence.video_id, association.target_evidence.seconds)">{{ formatRange(association.target_evidence.start_ms, association.target_evidence.end_ms) }}</button></li></ul>
            <p v-else class="node-panel__muted">暂无其他视频中的同一知识对象</p>
            <p v-if="crossVideoRejectedCount" class="node-panel__muted">另有 {{ crossVideoRejectedCount }} 条候选未通过证据校验</p>
          </section>
        </article>
      </aside>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted } from 'vue'
import { FALLBACK_ATTRIBUTE_COLOR, KNOWN_ATTRIBUTES } from './graphStyles'
import { getGraphRelationTypeLabel, getRelationDescription } from './knowledgeTypeStyles'
import type { CrossVideoAssociation, CrossVideoStatus, GraphEdge, GraphEvidence, GraphKnowledgeDetail, GraphNode, GraphReadingAssociation, KnowledgeGraphStatus } from '@/types/videohub'

const props = defineProps<{ node: GraphNode; detail?: GraphKnowledgeDetail | null; evidence?: GraphEvidence[]; relatedNodes: GraphNode[]; relatedEdges: GraphEdge[]; readingAssociations: GraphReadingAssociation[]; detailLoading?: boolean; detailLoaded?: boolean; detailStatus?: KnowledgeGraphStatus | string; detailError?: string; crossVideoLoading?: boolean; crossVideoStatus?: CrossVideoStatus | string; crossVideoAssociations?: CrossVideoAssociation[]; crossVideoRejectedCount?: number; crossVideoError?: string }>()
type GraphNodeTarget = { targetPageId: string; title: string; targetSlug?: string }

const emit = defineEmits<{ close: []; retryDetail: []; retryCrossVideo: []; selectVideoById: [videoId: string, seconds: number]; selectGraphNode: [target: GraphNodeTarget] }>()

const detail = computed(() => props.detail || null)
const title = computed(() => detail.value?.title || props.node.label)
const attribute = computed(() => props.node.attributes[0] || '')
const attributeColor = computed(() => `var(${KNOWN_ATTRIBUTES[attribute.value] ?? FALLBACK_ATTRIBUTE_COLOR})`)
const isEntity = computed(() => detail.value?.knowledge_type === 'entity' || props.node.knowledge_type === 'entity')
const typeLabel = computed(() => detail.value?.entity_sub_type
  ? ({ person: '人物', organization: '机构', product: '产品', technology: '技术', industry: '行业', place: '地点' }[detail.value.entity_sub_type] || '实体')
  : ({ entity: '实体', concept: '概念', case: '案例', methodology: '方法论', insight: '洞察' }[detail.value?.knowledge_type || attribute.value] || attribute.value || '未知类型'))
const firstEvidence = computed(() => props.evidence?.[0])
const evidenceCount = computed(() => props.evidence?.length || 0)
const sourceVideoId = computed(() => detail.value?.video_id || firstEvidence.value?.video_id || props.node.video_id || '')
const sourceVideoTitle = computed(() => detail.value?.source_video_title || detail.value?.video_title || firstEvidence.value?.video_title || props.node.video_title || '')
const validReading = computed(() => props.readingAssociations.filter(item => item.target_exists))
function pageIdFromNodeId(value: string) { return value.trim().replace(/^wiki:/, '') }
const formalLinks = computed(() => props.relatedEdges.map((edge, index) => {
  const outgoing = edge.source === props.node.id
  const targetId = outgoing ? edge.target : edge.source
  const target = props.relatedNodes.find(item => item.id === targetId)
  const endpointTitle = outgoing ? edge.target_title : edge.source_title
  const endpointSlug = outgoing ? edge.target_slug : edge.source_slug
  const title = endpointTitle || target?.knowledge_detail?.title || target?.label || '关联知识页面'
  const targetPageId = pageIdFromNodeId(target?.wiki_page_id || targetId)
  return { key: edge.id || `${targetId}-${index}`, title, description: getRelationDescription(title, edge.type, outgoing), targetPageId, targetSlug: endpointSlug || target?.knowledge_detail?.slug, type: getGraphRelationTypeLabel(edge.type), confidence: edge.confidence }
}))
const readingLinks = computed(() => validReading.value.filter(item => item.target_title).map(item => ({ key: item.id, title: item.target_title || '', description: `当前知识与「${item.target_title}」存在阅读关联`, targetPageId: pageIdFromNodeId(item.target), targetSlug: item.target_slug })))
const detailStatusLabel = computed(() => ({ partial: '部分关系或证据未通过校验，以下仅展示可用内容。', not_projected: 'Wiki 页面可读，但尚未形成图谱投影。' }[props.detailStatus || ''] || `页面状态：${props.detailStatus}`))
const crossVideoLoading = computed(() => props.crossVideoLoading || false)
const crossVideoStatus = computed(() => props.crossVideoStatus || 'idle')
const crossVideoAssociations = computed(() => props.crossVideoAssociations || [])
const crossVideoRejectedCount = computed(() => props.crossVideoRejectedCount || 0)
const crossVideoError = computed(() => props.crossVideoError || '')

function formatTime(seconds: number) { const value = Math.max(0, Math.floor(seconds)); const hours = Math.floor(value / 3600); const minutes = Math.floor(value % 3600 / 60); const remainder = value % 60; return hours ? `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}` : `${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}` }
function formatRange(start: number, end: number) { return `${formatTime(start / 1000)}-${formatTime(end / 1000)}` }
function selectVideo(videoId: string, seconds: number) { if (videoId) emit('selectVideoById', videoId, seconds) }
function selectGraphNode(target: GraphNodeTarget) { if (target.targetPageId) emit('selectGraphNode', target) }
onMounted(() => nextTick(() => document.querySelector<HTMLElement>('.node-panel')?.focus()))
</script>

<style scoped>
.node-panel-layer { position: fixed; z-index: 3000; inset: 0; }
.node-panel-mask { position: absolute; inset: 0; width: 100%; border: 0; background: color-mix(in srgb, var(--td-text-color-primary) 34%, transparent); cursor: default; }
.node-panel { position: absolute; top: 0; right: 0; bottom: 0; box-sizing: border-box; width: min(680px, 96vw); padding: 0 24px 24px; overflow-y: auto; border: 1px solid var(--td-component-stroke); border-right: 0; border-radius: var(--td-radius-large) 0 0 var(--td-radius-large); outline: none; background: var(--td-bg-color-container); box-shadow: var(--td-shadow-2); animation: panel-in .18s ease-out; }
.node-panel__header { position: sticky; z-index: 2; top: 0; display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin: 0 -24px; padding: 24px; border-bottom: 1px solid var(--td-component-stroke); background: var(--td-bg-color-container); }
.node-panel h2 { max-width: 22ch; margin: 8px 0 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-large); font-weight: 600; line-height: var(--td-line-height-title-large); overflow-wrap: anywhere; }
.node-panel__tag { display: inline-flex; align-items: center; min-height: 20px; padding: 0 8px; border: 1px solid; border-radius: var(--td-radius-round); background: var(--td-bg-color-secondarycontainer); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); }
.node-panel__article { max-width: 72ch; margin: 0 auto; }
.node-panel__lead { margin: 24px 0 16px; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-large); font-weight: 500; line-height: var(--td-line-height-body-large); }
.node-panel__meta { display: flex; flex-wrap: wrap; gap: 8px 16px; padding-bottom: 20px; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); }
.node-panel__meta span, .node-panel__meta button { display: inline-flex; align-items: center; gap: 4px; }
.node-panel__section { padding: 20px 0; border-top: 1px solid var(--td-component-stroke); }
.node-panel__paragraph + .node-panel__paragraph { margin-top: 16px; }
.node-panel__section h3, .node-panel__paragraph h3 { margin: 0 0 8px; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-small); font-weight: 600; line-height: var(--td-line-height-title-small); }
.node-panel__section h3 small { margin-left: 4px; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); font-weight: 400; line-height: var(--td-line-height-body-small); }
.node-panel__relation-group + .node-panel__relation-group { margin-top: 20px; }
.node-panel__relation-group h4 { margin: 0 0 8px; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); font-weight: 600; line-height: var(--td-line-height-body-medium); }
.node-panel__relation-group h4 small { margin-left: 4px; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); font-weight: 400; }
.node-panel__paragraph p, .node-panel__section > p { margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: var(--td-line-height-body-medium); white-space: pre-line; overflow-wrap: anywhere; }
.node-panel__links { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
.node-panel__links li { display: grid; gap: 4px; min-width: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: var(--td-line-height-body-medium); }
.node-panel__links button { width: fit-content; max-width: 100%; padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; text-align: left; cursor: pointer; overflow-wrap: anywhere; }
.node-panel__links small { color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); }
.node-panel blockquote { margin: 0; padding: 0 0 0 16px; border-left: 2px solid color-mix(in srgb, var(--td-brand-color) 42%, transparent); }
.node-panel blockquote + blockquote { margin-top: 16px; }
.node-panel blockquote p { margin: 0 0 8px; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: var(--td-line-height-body-medium); }
.node-panel__time { padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; cursor: pointer; }
.node-panel__time:hover, .node-panel__links button:hover { text-decoration: underline; }
.node-panel__state { display: flex; align-items: center; justify-content: center; gap: 8px; min-height: 240px; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: var(--td-line-height-body-medium); }
.node-panel__failure { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding-top: 24px; color: var(--td-error-color); }
.node-panel__notice { margin-top: 16px; color: var(--td-warning-color); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); }
.node-panel__muted { color: var(--td-text-color-placeholder) !important; }
@keyframes panel-in { from { transform: translateX(20px); opacity: 0; } }
@media (max-width: 640px) { .node-panel { width: 100vw; padding-right: 16px; padding-left: 16px; border-radius: 0; }.node-panel__header { margin-right: -16px; margin-left: -16px; padding: 16px; }.node-panel__lead { margin-top: 20px; } }
</style>
