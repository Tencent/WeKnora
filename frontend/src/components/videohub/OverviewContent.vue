<template>
  <section class="overview-content" aria-label="视频概要">
    <header class="overview-content__heading"><h2>快速概览</h2></header>
    <div v-if="loading" class="overview-content__state"><t-loading text="正在加载概要" /></div>
    <t-alert v-else-if="error" class="overview-content__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="!content" class="overview-content__state" :description="notGenerated ? '概要尚未生成' : '暂无概要'">
      <template #action><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-empty>
    <p v-else class="overview-content__body">{{ content }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ContentState } from '@/types/videohub'

const props = defineProps<{ contentState: ContentState<string> }>()
const emit = defineEmits<{ reload: [] }>()
const content = computed(() => props.contentState.data.trim())
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '概要加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
function load() { emit('reload') }
</script>

<style scoped>
.overview-content { overflow: hidden; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.overview-content__heading { padding: 12px 16px; border-bottom: 1px solid var(--td-border-level-1-color); }
.overview-content__heading h2 { margin: 0; color: var(--td-text-color-primary); font-size: 16px; font-weight: 400; line-height: 1.5; }
.overview-content__state { min-height: 150px; display: grid; place-items: center; }
.overview-content__body { margin: 0; padding: 16px; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: 1.75; white-space: pre-wrap; }
</style>
