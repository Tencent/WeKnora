<template>
  <div class="assistant-tool-result weknora-doc-result">
    <p v-if="success === false || data.error" class="result-error" role="alert">{{ toolText(data.error) || output || $t('agentStream.assistantTools.failed') }}</p>
    <template v-if="search && (Array.isArray(data.pages) || Array.isArray(data.results))">
      <p class="result-meta">{{ $t('agentStream.assistantTools.documents', { count: rows.length }) }}</p>
      <p v-if="!rows.length" class="result-meta">{{ $t('agentStream.assistantTools.empty') }}</p>
      <article v-for="(row, index) in rows" :key="index" class="result-card">
        <a v-if="assistantDocURL(row.url)" :href="assistantDocURL(row.url)" target="_blank" rel="noopener noreferrer">{{ row.section || row.title || row.path }}</a>
        <span v-else>{{ row.section || row.title || row.path }}</span>
        <p class="result-meta">{{ row.path }}<span v-if="row.category"> · {{ row.category }}</span></p>
        <p v-if="row.snippet">{{ row.snippet }}</p>
      </article>
    </template>
    <template v-else-if="!search && (data.content !== undefined || Array.isArray(data.outline))">
      <div class="result-heading">
        <a v-if="assistantDocURL(data.url)" :href="assistantDocURL(data.url)" target="_blank" rel="noopener noreferrer">{{ data.section || data.title || data.path }}</a>
        <span v-else>{{ data.section || data.title || data.path }}</span>
      </div>
      <p v-if="data.path" class="result-meta">{{ data.path }}</p>
      <div v-if="content" class="result-body doc-content md-content" v-html="content" />
      <details v-if="outline.length" open>
        <summary>{{ $t('agentStream.assistantTools.outline') }}</summary>
        <ul class="doc-outline">
          <li v-for="(entry, index) in outline" :key="index">
            <a v-if="assistantDocURL(data.url, entry.anchor)" :href="assistantDocURL(data.url, entry.anchor)" target="_blank" rel="noopener noreferrer">{{ entry.heading }}</a>
            <span v-else>{{ entry.heading }}</span>
          </li>
        </ul>
      </details>
      <p v-if="data.truncated === true" class="result-warning">{{ $t('agentStream.assistantTools.truncated') }}<span v-if="typeof data.next_offset === 'number'"> · {{ $t('agentStream.assistantTools.continueAt', { offset: data.next_offset }) }}</span></p>
    </template>
    <pre v-else-if="success !== false && !data.error" class="result-body">{{ output || $t('agentStream.assistantTools.empty') }}</pre>
    <p v-if="data.hint" class="result-meta">{{ data.hint }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { assistantDocURL, parseAssistantToolOutput, toolRows, toolText } from '@/utils/assistantToolDisplay'
import { renderDocumentPreviewMarkdown } from '@/utils/documentPreviewMarkdown'

const props = withDefaults(defineProps<{
  search: boolean; output: string; data?: Record<string, unknown>; success?: boolean
}>(), { success: undefined })
const data = computed(() => parseAssistantToolOutput(props.output, props.data))
const rows = computed(() => toolRows(data.value.pages ?? data.value.results))
const outline = computed(() => toolRows(data.value.outline))
const content = computed(() => renderDocumentPreviewMarkdown(toolText(data.value.content)))
</script>

<style scoped lang="less">
@import './assistant-tool-result.less';
@import '@/components/css/markdown.less';
.doc-outline { padding-left: 20px; }
.doc-outline li + li { margin-top: 6px; }
.doc-content { :deep(img) { max-width: 100%; } :deep(pre) { overflow-x: auto; } :deep(table) { border-collapse: collapse; } :deep(th), :deep(td) { border: 1px solid var(--td-component-stroke); padding: 4px 8px; } }
</style>
