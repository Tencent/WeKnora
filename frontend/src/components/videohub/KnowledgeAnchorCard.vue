<template>
  <article class="knowledge-anchor">
    <header>
      <div class="knowledge-anchor__main">
        <h3>{{ anchor.content }}</h3>
      </div>
      <button class="knowledge-anchor__time" type="button" title="定位到当前视频此位置" @click="emit('seek', anchor.seconds)">{{ anchor.timestamp }}</button>
    </header>
    <p v-if="anchor.coreContent" class="knowledge-anchor__core">{{ anchor.coreContent }}</p>
    <dl v-if="anchor.structureFields?.length" class="knowledge-anchor__fields">
      <template v-for="field in anchor.structureFields" :key="field.key">
        <dt>{{ field.label }}</dt>
        <dd>{{ field.value }}</dd>
      </template>
    </dl>
    <div v-if="anchor.evidenceIds?.length || anchor.informationNature" class="knowledge-anchor__evidence">
      <span v-for="id in anchor.evidenceIds" :key="id" class="knowledge-anchor__pill">{{ id }}</span>
      <span v-if="anchor.informationNature" class="knowledge-anchor__nature">{{ anchor.informationNature }}</span>
    </div>
    <p class="knowledge-anchor__meta">关联 {{ items.length }} 条知识</p>
    <div v-if="items.length" class="knowledge-anchor__relations">
      <CrossVideoRelationItem v-for="item in items" :key="item.id" :item="item" @select-video-by-id="forwardSelection" />
    </div>
  </article>
</template>

<script setup lang="ts">
import CrossVideoRelationItem from './CrossVideoRelationItem.vue'
import type { CrossVideoKnowledgeItem, CurrentKnowledgeAnchor } from '@/types/videohub'

defineProps<{ anchor: CurrentKnowledgeAnchor; items: CrossVideoKnowledgeItem[] }>()
const emit = defineEmits<{ seek: [seconds: number]; selectVideoById: [videoId: string, seconds: number] }>()
function forwardSelection(videoId: string, seconds: number) { emit('selectVideoById', videoId, seconds) }
</script>

<style scoped>
.knowledge-anchor { padding: calc(var(--td-comp-margin-s) * 2); border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.knowledge-anchor header { display: flex; align-items: flex-start; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 1.5); }
.knowledge-anchor__main { display: flex; align-items: flex-start; gap: var(--td-comp-margin-s); min-width: 0; }
.knowledge-anchor h3 { margin: 0; color: var(--td-text-color-primary); font-size: 15px; font-weight: 600; line-height: 1.55; }
.knowledge-anchor__time { flex: none; padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font-family: var(--app-font-family-mono, monospace); font-size: var(--td-font-size-body-small); cursor: pointer; }
.knowledge-anchor__time:hover { text-decoration: underline; }
.knowledge-anchor__core { margin: var(--td-comp-margin-s) 0 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-medium); line-height: 1.6; }
.knowledge-anchor__fields { display: grid; grid-template-columns: minmax(72px, max-content) minmax(0, 1fr); gap: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); margin: calc(var(--td-comp-margin-s) * 1.5) 0 0; padding: var(--td-comp-margin-s); border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); }
.knowledge-anchor__fields dt { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); white-space: nowrap; }
.knowledge-anchor__fields dd { min-width: 0; margin: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-small); line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere; }
.knowledge-anchor__evidence { display: flex; flex-wrap: wrap; gap: calc(var(--td-comp-margin-s) / 2); margin-top: var(--td-comp-margin-s); }
.knowledge-anchor__pill, .knowledge-anchor__nature { display: inline-flex; align-items: center; min-height: 20px; padding: 0 5px; border-radius: 999px; font-size: 11.5px; line-height: 1.45; }
.knowledge-anchor__pill { background: color-mix(in srgb, var(--td-text-color-primary) 4%, transparent); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 10%, transparent); color: var(--td-text-color-secondary); }
.knowledge-anchor__nature { background: color-mix(in srgb, var(--td-brand-color) 6%, transparent); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--td-brand-color) 17%, transparent); color: color-mix(in srgb, var(--td-brand-color) 72%, var(--td-text-color-secondary)); }
.knowledge-anchor__meta { margin: var(--td-comp-margin-s) 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.knowledge-anchor__relations { display: grid; gap: calc(var(--td-comp-margin-s) * 2); margin-top: calc(var(--td-comp-margin-s) * 2); }
@media (max-width: 640px) { .knowledge-anchor__fields { grid-template-columns: 1fr; } }
</style>
