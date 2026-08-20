<template>
  <article class="cross-relation">
    <div class="cross-relation__phrase">{{ phrase }}</div>
    <div class="cross-relation__content">{{ item.knowledge_content }}</div>
    <p>{{ item.relation_description }}</p>
    <footer>
      <t-tooltip :content="item.video_title">
        <button class="cross-relation__video" type="button" @click="navigate">《{{ normalizedTitle }}》</button>
      </t-tooltip>
      <button class="cross-relation__time" type="button" title="查看并定位此关联知识" @click="navigate">{{ item.timestamp }}</button>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getNaturalRelationPhrase } from './knowledgeTypeStyles'
import type { CrossVideoKnowledgeItem } from '@/types/videohub'

const props = defineProps<{ item: CrossVideoKnowledgeItem }>()
const emit = defineEmits<{ selectVideoById: [videoId: string, seconds: number] }>()
const phrase = computed(() => getNaturalRelationPhrase(props.item.knowledge_type, props.item.relation_type))
const normalizedTitle = computed(() => props.item.video_title.replace(/^《+|》+$/g, ''))
function navigate() { emit('selectVideoById', props.item.video_id, props.item.seconds) }
</script>

<style scoped>
.cross-relation { display: grid; gap: calc(var(--td-comp-margin-s) * .75); padding-top: calc(var(--td-comp-margin-s) * 2); border-top: 1px solid var(--td-component-stroke); }
.cross-relation__phrase { color: var(--td-text-color-secondary); font-size: 13px; font-weight: 600; }
.cross-relation__content { color: var(--td-text-color-primary); font-size: var(--td-font-size-body-medium); font-weight: 500; line-height: 1.55; }
.cross-relation p { margin: 0; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.6; }
.cross-relation footer { display: flex; align-items: center; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 1.5); min-width: 0; }
.cross-relation button { padding: 0; border: 0; background: transparent; font: inherit; cursor: pointer; }
.cross-relation__video { overflow: hidden; max-width: 200px; color: var(--td-text-color-secondary); text-overflow: ellipsis; white-space: nowrap; }
.cross-relation__video:hover, .cross-relation__time:hover { color: var(--td-brand-color); text-decoration: underline; }
.cross-relation__time { flex: none; color: var(--td-brand-color); font-family: var(--app-font-family-mono, monospace) !important; font-size: var(--td-font-size-body-small) !important; }
</style>
