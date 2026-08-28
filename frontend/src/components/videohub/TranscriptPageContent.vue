<template>
  <article class="transcript-page-content">
    <div v-if="loading" class="transcript-page-content__state"><t-loading text="正在加载完整文字稿" /></div>
    <t-alert v-else-if="error" class="transcript-page-content__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="!content" class="transcript-page-content__state" :description="notGenerated ? '完整文字稿尚未生成' : '暂无完整文字稿'">
      <template #action><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-empty>
    <div v-else class="transcript-page-content__body" v-html="renderedContent" />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import { sanitizeMarkdownHTML } from '@/utils/security'
import type { ContentState } from '@/types/videohub'

const props = defineProps<{ contentState: ContentState<string> }>()
const emit = defineEmits<{ reload: [] }>()
const content = computed(() => props.contentState.data.trim())
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '完整文字稿加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const renderedContent = computed(() => sanitizeMarkdownHTML(marked.parse(content.value, { breaks: true, async: false }) as string))
function load() { emit('reload') }
</script>

<style scoped>
.transcript-page-content { min-height: 360px; }
.transcript-page-content__state { min-height: 360px; display: grid; place-items: center; }
.transcript-page-content__body { padding: 16px; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: 1.75; overflow-wrap: anywhere; }
.transcript-page-content__body :deep(h1), .transcript-page-content__body :deep(h2), .transcript-page-content__body :deep(h3) { color: var(--td-text-color-primary); line-height: 1.45; }
.transcript-page-content__body :deep(blockquote) { margin: 12px 0; padding-left: 12px; border-left: 3px solid var(--td-brand-color); color: var(--td-text-color-secondary); }
.transcript-page-content__body :deep(pre) { overflow: auto; padding: 12px; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); }
</style>
