<template>
  <div class="doc-result">
    <div v-if="plainError" class="doc-error" role="alert">
      <t-icon name="error-circle" />
      <pre>{{ plainError }}</pre>
    </div>

    <template v-else-if="mode === 'search'">
      <div class="doc-summary">
        {{ listingPages
          ? $t('agentStream.weknoraDocs.pages', { count: pages.length })
          : $t('agentStream.weknoraDocs.hits', { count: hits.length }) }}
      </div>
      <p v-if="!rows.length" class="doc-empty">{{ $t('agentStream.weknoraDocs.empty') }}</p>
      <div v-else class="doc-list">
        <article v-for="(row, index) in rows" :key="`${row.path}-${index}`" class="doc-item">
          <div class="doc-item__head">
            <span class="doc-item__index">#{{ index + 1 }}</span>
            <a
              v-if="row.href"
              class="doc-item__title"
              :href="row.href"
              target="_blank"
              rel="noopener noreferrer"
            >{{ row.title }}</a>
            <span v-else class="doc-item__title">{{ row.title }}</span>
            <span v-if="row.category" class="doc-item__meta">{{ row.category }}</span>
          </div>
          <p v-if="row.snippet" class="doc-item__snippet">{{ row.snippet }}</p>
          <p v-if="row.locator" class="doc-item__path">{{ row.locator }}</p>
        </article>
      </div>
    </template>

    <template v-else>
      <div v-if="read.error" class="doc-error" role="alert">
        <t-icon name="error-circle" />
        <pre>{{ read.error }}</pre>
      </div>
      <div v-if="read.title || read.url" class="doc-read__head">
        <span v-if="read.title" class="doc-read__title">{{ read.title }}</span>
        <a v-if="readHref" class="doc-read__link" :href="readHref" target="_blank" rel="noopener noreferrer">
          {{ $t('agentStream.weknoraDocs.openDoc') }}
        </a>
      </div>
      <p v-if="read.section" class="doc-read__section">{{ read.section }}</p>
      <div v-if="read.outline.length" class="doc-outline">
        <div class="doc-summary">{{ $t('agentStream.weknoraDocs.outline') }}</div>
        <ul>
          <li
            v-for="(entry, index) in read.outline"
            :key="`${entry.anchor}-${index}`"
            :style="{ paddingLeft: `${Math.max(0, entry.level - 2) * 12}px` }"
          >{{ entry.heading }}</li>
        </ul>
      </div>
      <div v-if="read.content" class="doc-read__content">
        <div class="doc-summary">{{ $t('agentStream.weknoraDocs.content') }}</div>
        <DocMarkdown :source="read.content" />
        <p v-if="read.truncated" class="doc-note">{{ $t('agentStream.weknoraDocs.truncated') }}</p>
      </div>
      <p v-else-if="!read.outline.length && !read.error" class="doc-empty">{{ $t('agentStream.weknoraDocs.empty') }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  docHits,
  docPages,
  docReadView,
  parseCatalogPayload,
  safeDocUrl,
} from '@/utils/catalogToolDisplay'
import DocMarkdown from './DocMarkdown.vue'

const props = withDefaults(defineProps<{
  mode: 'search' | 'read'
  output?: string
  data?: Record<string, unknown>
  success?: boolean
}>(), { output: '', success: undefined })

const { t } = useI18n()
const payload = computed(() => parseCatalogPayload(props.output, props.data))
const pages = computed(() => docPages(payload.value))
const hits = computed(() => docHits(payload.value))
const read = computed(() => docReadView(payload.value))
const readHref = computed(() => safeDocUrl(read.value.url))
const listingPages = computed(() => Array.isArray(payload.value.pages))
const rows = computed(() => {
  const source = listingPages.value
    ? pages.value.map((page) => ({ ...page, anchor: '', snippet: '' }))
    : hits.value
  return source.map((row) => ({
    ...row,
    href: safeDocUrl(row.url),
    locator: row.anchor ? `${row.path}#${row.anchor}` : row.path,
  }))
})
const structured = computed(() => (
  props.mode === 'search'
    ? Array.isArray(payload.value.pages) || Array.isArray(payload.value.results)
    : Boolean(read.value.title || read.value.path || read.value.content || read.value.outline.length || read.value.error)
))
const plainError = computed(() => {
  if (props.success !== false || structured.value) return ''
  return props.output.trim() || t('agentStream.weknoraDocs.failed')
})
</script>

<style lang="less" scoped>
.doc-result { min-width: 0; font-size: var(--app-text-sm); color: var(--td-text-color-primary); }
.doc-summary { margin-bottom: 8px; color: var(--td-text-color-secondary); }
.doc-empty, .doc-note { margin: 0; color: var(--td-text-color-placeholder); }
.doc-note { margin-top: 8px; }
.doc-list {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  overflow: hidden;
  background: var(--td-bg-color-container);
  max-height: 360px;
  overflow-y: auto;
}
.doc-item + .doc-item { border-top: 1px solid var(--td-component-stroke); }
.doc-item { padding: 8px 10px; }
.doc-item__head { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
.doc-item__index {
  flex-shrink: 0;
  min-width: 22px;
  font-size: var(--app-text-xs);
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}
.doc-item__title {
  flex: 1;
  min-width: 0;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
a.doc-item__title { color: var(--td-brand-color); text-decoration: none; }
a.doc-item__title:hover { text-decoration: underline; }
.doc-item__meta, .doc-item__path { flex-shrink: 0; font-size: var(--app-text-xs); color: var(--td-text-color-placeholder); }
.doc-item__snippet, .doc-item__path { margin: 4px 0 0 30px; }
.doc-item__snippet {
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.doc-read__head { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px 12px; margin-bottom: 6px; }
.doc-read__title { font-weight: 600; }
.doc-read__link { color: var(--td-brand-color); }
.doc-read__section { margin: 0 0 8px; color: var(--td-text-color-secondary); }
.doc-outline ul { margin: 0; padding: 0; list-style: none; max-height: 220px; overflow: auto; }
.doc-outline li { padding: 2px 0; color: var(--td-text-color-secondary); }
.doc-read__content { margin-top: 8px; }
.doc-error { display: flex; align-items: flex-start; gap: 8px; margin-bottom: 8px; color: var(--td-error-color); }
.doc-error pre {
  flex: 1;
  min-width: 0;
  margin: 0;
  padding: 0;
  background: transparent;
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
