<template>
  <teleport to="body">
    <div class="node-panel-layer" @keydown.esc="emit('close')">
      <button class="node-panel-mask" type="button" aria-label="关闭节点详情" @click="emit('close')" />
      <aside class="node-panel" role="dialog" aria-modal="true" aria-labelledby="node-panel-title" tabindex="-1">
        <header>
          <div>
            <span class="node-panel__tag" :style="{ color: attributeColor, borderColor: attributeColor }">{{ node.attributes[0] || '无分类' }}</span>
            <h2 id="node-panel-title">{{ node.label }}</h2>
          </div>
          <t-button variant="text" shape="square" aria-label="关闭" @click="emit('close')"><t-icon name="close" /></t-button>
        </header>
        <p class="node-panel__summary">{{ node.name }}</p>
        <section>
          <h3>属性</h3>
          <ul v-if="node.attributes.length"><li v-for="attribute in node.attributes" :key="attribute">{{ attribute }}</li></ul>
          <p v-else class="node-panel__muted">（无分类）</p>
        </section>
        <section>
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
          <h3>关联信息</h3>
          <p>关联 {{ relatedEdges.length }} 条边 / {{ relatedVideos.length }} 个其他视频</p>
          <ul class="node-panel__videos">
            <li v-for="related in relatedVideos" :key="related.id">
              <span>{{ related.video_title }}</span>
              <button type="button" @click="selectVideo(related.video_id!, related.seconds)">{{ formatTime(related.seconds) }}</button>
            </li>
          </ul>
        </section>
      </aside>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted } from 'vue'
import { FALLBACK_ATTRIBUTE_COLOR, KNOWN_ATTRIBUTES } from './graphStyles'
import type { GraphEdge, GraphNode } from '@/types/videohub'

const props = defineProps<{ node: GraphNode; relatedNodes: GraphNode[]; relatedEdges: GraphEdge[] }>()
const emit = defineEmits<{ close: []; selectVideoById: [videoId: string, seconds: number] }>()
const attributeColor = computed(() => `var(${KNOWN_ATTRIBUTES[props.node.attributes[0]] ?? FALLBACK_ATTRIBUTE_COLOR})`)
const relatedVideos = computed(() => {
  const seen = new Set<string>()
  return props.relatedNodes.filter(item => item.video_id && item.video_id !== props.node.video_id && !seen.has(item.video_id) && Boolean(seen.add(item.video_id)))
})
function formatTime(seconds = 0) { const value = Math.max(0, Math.floor(seconds)); return `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}` }
function formatRange(startMs: number, endMs: number) { return `${formatTime(startMs / 1000)} - ${formatTime(endMs / 1000)}` }
function selectVideo(videoId: string, seconds = 0) { emit('selectVideoById', videoId, seconds) }
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
.node-panel section p, .node-panel__summary { color: var(--td-text-color-secondary); line-height: 1.65; }
.node-panel__tag { display: inline-flex; padding: calc(var(--td-comp-margin-s) / 4) var(--td-comp-margin-s); border: var(--border-width-hairline, .5px) solid; border-radius: var(--td-radius-round); background: var(--td-bg-color-secondarycontainer); font-size: var(--td-font-size-body-small); }
.node-panel ul { margin: 0; padding-left: calc(var(--td-comp-margin-s) * 2); color: var(--td-text-color-secondary); }
.node-panel__time, .node-panel__videos button { padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; cursor: pointer; }
.node-panel__videos { display: grid; gap: var(--td-comp-margin-s); padding: 0 !important; list-style: none; }
.node-panel__evidence { display: grid; gap: var(--td-comp-margin-s); padding: 0 !important; list-style: none; }
.node-panel__evidence li { display: flex; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); }
.node-panel__evidence span { overflow: hidden; color: var(--td-text-color-primary); text-overflow: ellipsis; white-space: nowrap; }
.node-panel__videos li { display: flex; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); }
.node-panel__videos span { overflow: hidden; color: var(--td-text-color-primary); text-overflow: ellipsis; white-space: nowrap; }
.node-panel__muted { color: var(--td-text-color-placeholder) !important; }
@keyframes panel-in { from { transform: translateX(20px); opacity: 0; } }
</style>
