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
        <p class="node-panel__meta-line">信息性质：{{ detailTypeLabel }}<span v-if="detail?.information_nature"> / {{ detail.information_nature }}</span></p>
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
        <section v-if="sourceRanges.length || detail?.evidence_ids?.length || detail?.information_nature">
          <h3>原文证据</h3>
          <div v-if="sourceRanges.length" class="node-panel__source-ranges">
            <button v-for="range in sourceRanges" :key="range.key" class="node-panel__time" type="button" @click="selectVideo(range.videoId, range.seconds)">{{ range.label }}</button>
          </div>
          <div class="node-panel__evidence-tags">
            <span v-for="id in detail?.evidence_ids" :key="id" class="node-panel__pill">{{ id }}</span>
            <span v-if="detail?.information_nature" class="node-panel__nature">{{ detail.information_nature }}</span>
          </div>
        </section>
        <section v-else>
          <h3>原文证据</h3>
          <p class="node-panel__muted">当前节点缺少可定位的原文时间戳</p>
        </section>
        <section v-if="node.evidence?.length || node.video_id">
          <h3>来源视频</h3>
          <template v-if="node.evidence?.length">
            <ul class="node-panel__evidence">
              <li v-for="evidence in node.evidence" :key="`${evidence.video_id}-${evidence.chunk_index}`">
                <span>{{ evidence.video_title || node.video_title || '未命名视频' }}</span>
                <button class="node-panel__time" type="button" @click="selectVideo(evidence.video_id, evidence.start_ms / 1000)">
                  {{ formatRange(evidence.start_ms, evidence.end_ms) }}
                </button>
              </li>
            </ul>
          </template>
          <template v-else-if="node.video_id">
            <p>{{ node.video_title }}</p>
            <button class="node-panel__time" type="button" @click="selectVideo(node.video_id, node.seconds)">{{ formatTime(node.seconds) }}</button>
          </template>
          <p v-else class="node-panel__muted">未关联视频</p>
        </section>
        <section>
          <h3>关联知识</h3>
          <ul v-if="relatedKnowledgeLinks.length" class="node-panel__links">
            <li v-for="link in relatedKnowledgeLinks" :key="link.key">
              <span>{{ link.title }}</span>
              <small v-if="link.relation">{{ link.relation }}</small>
            </li>
          </ul>
          <p v-else class="node-panel__muted">暂无关联知识</p>
        </section>
        <section>
          <h3>关联实体</h3>
          <ul v-if="relatedEntityLinks.length" class="node-panel__links">
            <li v-for="link in relatedEntityLinks" :key="link.key">
              <span>{{ link.title }}</span>
              <small v-if="link.relation">{{ link.relation }}</small>
            </li>
          </ul>
          <p v-else class="node-panel__muted">暂无关联实体</p>
        </section>
      </aside>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted } from 'vue'
import { FALLBACK_ATTRIBUTE_COLOR, KNOWN_ATTRIBUTES } from './graphStyles'
import type { GraphEdge, GraphNode, WikiDetailLink } from '@/types/videohub'

const props = defineProps<{ node: GraphNode; relatedNodes: GraphNode[]; relatedEdges: GraphEdge[] }>()
const emit = defineEmits<{ close: []; selectVideoById: [videoId: string, seconds: number] }>()
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
const knowledgeTypeLabels: Record<string, string> = { entity: '实体', concept: '概念', case: '案例', method: '方法', insight: '洞察' }
const entitySubTypeLabels: Record<string, string> = { person: '人物', organization: '机构', product: '产品', technology: '技术', industry: '行业', place: '地点' }
const sourceRanges = computed(() => {
  const ranges = (props.node.evidence || []).map((item, index) => ({
    key: `${item.video_id}-${item.chunk_index}-${index}`,
    label: formatRange(item.start_ms, item.end_ms),
    videoId: item.video_id,
    seconds: item.start_ms / 1000,
  }))
  if (ranges.length || !detail.value?.time_range || !props.node.video_id) return ranges
  return [{ key: `${props.node.video_id}-detail-range`, label: detail.value.time_range, videoId: props.node.video_id, seconds: props.node.seconds || 0 }]
})
const relatedKnowledgeLinks = computed(() => mergeRelatedLinks(detail.value?.related_knowledge || [], false))
const relatedEntityLinks = computed(() => mergeRelatedLinks(detail.value?.related_entities || [], true))
function formatTime(seconds = 0) { const value = Math.max(0, Math.floor(seconds)); return `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}` }
function formatRange(startMs: number, endMs: number) { return `${formatTime(startMs / 1000)} - ${formatTime(endMs / 1000)}` }
function selectVideo(videoId: string, seconds = 0) { emit('selectVideoById', videoId, seconds) }
function edgeForNode(nodeId: string) { return props.relatedEdges.find(edge => edge.source === nodeId || edge.target === nodeId) }
function mergeRelatedLinks(explicitLinks: WikiDetailLink[], entitiesOnly: boolean) {
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
    const nodeIsEntity = node.type === '实体' || node.attributes.includes('实体') || node.knowledge_detail?.knowledge_type === 'entity'
    if (nodeIsEntity !== entitiesOnly) return
    const edge = edgeForNode(node.id)
    add(node.knowledge_detail?.title || node.label || node.name, node.knowledge_detail?.slug, edge?.type)
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
.node-panel section p, .node-panel__summary { color: var(--td-text-color-secondary); line-height: 1.65; }
.node-panel__tag { display: inline-flex; padding: calc(var(--td-comp-margin-s) / 4) var(--td-comp-margin-s); border: var(--border-width-hairline, .5px) solid; border-radius: var(--td-radius-round); background: var(--td-bg-color-secondarycontainer); font-size: var(--td-font-size-body-small); }
.node-panel ul { margin: 0; padding-left: calc(var(--td-comp-margin-s) * 2); color: var(--td-text-color-secondary); }
.node-panel__fields { display: grid; grid-template-columns: minmax(76px, max-content) minmax(0, 1fr); gap: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); margin: 0; padding: var(--td-comp-margin-s); border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); }
.node-panel__fields dt { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); white-space: nowrap; }
.node-panel__fields dd { min-width: 0; margin: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-small); line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere; }
.node-panel__evidence-tags { display: flex; flex-wrap: wrap; gap: calc(var(--td-comp-margin-s) / 2); }
.node-panel__source-ranges { display: flex; flex-wrap: wrap; gap: var(--td-comp-margin-s); margin-bottom: var(--td-comp-margin-s); }
.node-panel__pill, .node-panel__nature { display: inline-flex; align-items: center; min-height: 20px; padding: 0 5px; border-radius: 999px; font-size: 11.5px; line-height: 1.45; }
.node-panel__pill { background: color-mix(in srgb, var(--td-text-color-primary) 4%, transparent); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 10%, transparent); color: var(--td-text-color-secondary); }
.node-panel__nature { background: color-mix(in srgb, var(--td-brand-color) 6%, transparent); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--td-brand-color) 17%, transparent); color: color-mix(in srgb, var(--td-brand-color) 72%, var(--td-text-color-secondary)); }
.node-panel__time { padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; cursor: pointer; }
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
