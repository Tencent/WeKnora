<template>
  <article class="knowledge-anchor">
    <header>
      <div class="knowledge-anchor__main">
        <h3>{{ anchor.content }}</h3>
      </div>
      <button class="knowledge-anchor__time" type="button" title="定位到当前视频此位置" @click="emit('seek', anchor.seconds)">{{ anchor.timestamp }}</button>
    </header>
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
.knowledge-anchor__meta { margin: var(--td-comp-margin-s) 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.knowledge-anchor__relations { display: grid; gap: calc(var(--td-comp-margin-s) * 2); margin-top: calc(var(--td-comp-margin-s) * 2); }
</style>
