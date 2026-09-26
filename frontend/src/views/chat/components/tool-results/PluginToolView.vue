<template>
  <div class="plugin-tool-view">
    <div v-if="success === false" class="plugin-tool-view__error" role="alert">
      <t-icon name="error-circle" />
      <pre>{{ output }}</pre>
    </div>

    <template v-else-if="spec.view === 'table'">
      <p v-if="!table.rows.length" class="plugin-tool-view__empty">{{ t('pluginToolView.empty') }}</p>
      <div v-else class="plugin-tool-view__table-wrap">
        <table class="plugin-tool-view__table">
          <thead>
            <tr><th v-for="(h, i) in table.headers" :key="i">{{ h }}</th></tr>
          </thead>
          <tbody>
            <tr v-for="(row, r) in table.rows" :key="r">
              <td v-for="(cell, c) in row" :key="c">
                <a v-if="cell.href" :href="cell.href" target="_blank" rel="noopener noreferrer">{{ cell.text }}</a>
                <template v-else>{{ cell.text }}</template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else-if="spec.view === 'cards'">
      <p v-if="!cards.length" class="plugin-tool-view__empty">{{ t('pluginToolView.empty') }}</p>
      <div v-else class="plugin-tool-view__cards">
        <article v-for="(card, i) in cards" :key="i" class="plugin-tool-view__card">
          <a v-if="card.href" class="plugin-tool-view__card-title" :href="card.href" target="_blank" rel="noopener noreferrer">{{ card.title }}</a>
          <div v-else class="plugin-tool-view__card-title">{{ card.title }}</div>
          <div v-if="card.subtitle" class="plugin-tool-view__card-subtitle">{{ card.subtitle }}</div>
          <p v-if="card.body" class="plugin-tool-view__card-body">{{ card.body }}</p>
        </article>
      </div>
    </template>

    <template v-else-if="spec.view === 'kv'">
      <div v-if="kv.title" class="plugin-tool-view__kv-title">
        <a v-if="kv.href" :href="kv.href" target="_blank" rel="noopener noreferrer">{{ kv.title }}</a>
        <template v-else>{{ kv.title }}</template>
      </div>
      <dl class="plugin-tool-view__kv">
        <template v-for="(row, i) in kv.rows" :key="i">
          <dt>{{ row.label }}</dt>
          <dd>
            <a v-if="row.href" :href="row.href" target="_blank" rel="noopener noreferrer">{{ row.text }}</a>
            <template v-else>{{ row.text }}</template>
          </dd>
        </template>
      </dl>
    </template>

    <!-- eslint-disable-next-line vue/no-v-html -- sanitized by renderDocumentPreviewMarkdown -->
    <div v-else-if="spec.view === 'markdown'" class="plugin-tool-view__markdown" v-html="markdownHtml" />

    <template v-else-if="spec.view === 'page' && page">
      <PluginFrame v-if="framePage" :page="framePage" :context="pageContext" />
      <p v-else-if="framePage === null" class="plugin-tool-view__empty">{{ t('pluginToolView.pageUnavailable') }}</p>
      <t-loading v-else size="small" />
    </template>

    <pre v-else class="plugin-tool-view__json">{{ json }}</pre>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import PluginFrame from '@/extensions/pluginFrame/PluginFrame.vue'
import type { FramePage } from '@/extensions/pluginFrame/pluginPages'
import {
  cardsModel,
  kvModel,
  markdownSource,
  tableModel,
  toolArguments,
  type PluginToolViewData,
  type ToolViewSpec,
} from '@/extensions/pluginToolView'
import { usePluginPagesStore } from '@/stores/pluginPages'
import { renderDocumentPreviewMarkdown } from '@/utils/documentPreviewMarkdown'

// A plugin tool's structured result, shown with the view its plugin
// declared in toolViews: generic views need no plugin code; a page view runs
// the plugin's own page in a sandboxed frame.
const props = defineProps<{
  data: PluginToolViewData & Record<string, unknown>
  output?: string
  arguments?: Record<string, unknown>
  success?: boolean
}>()

const { t, locale } = useI18n()

const spec = computed<ToolViewSpec>(() => props.data.plugin_view ?? { view: 'json' })
const structured = computed(() => props.data.structured)
const output = computed(() => props.output || '')

const table = computed(() => tableModel(spec.value, structured.value, locale.value))
const cards = computed(() => cardsModel(spec.value, structured.value))
const kv = computed(() => kvModel(spec.value, structured.value, locale.value))
const markdownHtml = computed(() =>
  renderDocumentPreviewMarkdown(markdownSource(spec.value, structured.value, output.value)),
)
const json = computed(() => {
  try {
    return JSON.stringify(structured.value, null, 2)
  } catch {
    return String(structured.value)
  }
})

const page = computed<FramePage | null>(() => {
  const d = props.data
  if (!d.plugin_id || !d.plugin_version || !d.mcp_server || !spec.value.entry) return null
  return {
    pluginId: d.plugin_id,
    version: d.plugin_version,
    // Requests from the page answer to the tool's server.
    mount: `mcpServers/${d.mcp_server}`,
    entry: spec.value.entry,
    name: { default: d.mcp_tool || d.plugin_id },
  }
})
// Only the loaded version of a plugin serves its page files: a result
// recorded before an upgrade shows under the current version, and one whose
// plugin is gone says so instead of waiting on a page that cannot load.
const pluginPages = usePluginPagesStore()
const pagesSettled = ref(false)
if (spec.value.view === 'page') {
  void pluginPages.ensure().catch(() => {}).finally(() => {
    pagesSettled.value = true
  })
}
/** undefined while the plugin listing loads; null when the plugin is gone. */
const framePage = computed<FramePage | null | undefined>(() => {
  if (!page.value || (!pluginPages.loaded && !pagesSettled.value)) return undefined
  return pluginPages.currentPage(page.value)
})
const pageContext = computed(() => ({
  tool: props.data.mcp_tool,
  arguments: toolArguments(props.arguments),
  result: structured.value,
}))
</script>

<style lang="less" scoped>
.plugin-tool-view {
  margin: 8px 0;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}

.plugin-tool-view__empty {
  margin: 0;
  color: var(--td-text-color-placeholder);
}

.plugin-tool-view__error {
  display: flex;
  gap: 8px;
  color: var(--td-error-color);

  pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
  }
}

.plugin-tool-view__table-wrap {
  max-height: 360px;
  overflow: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.plugin-tool-view__table {
  width: 100%;
  border-collapse: collapse;

  th,
  td {
    padding: 6px 10px;
    text-align: left;
    vertical-align: top;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  th {
    position: sticky;
    top: 0;
    font-weight: 500;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
  }

  tr:last-child td {
    border-bottom: none;
  }
}

.plugin-tool-view__cards {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.plugin-tool-view__card {
  padding: 8px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.plugin-tool-view__card-title {
  font-weight: 500;
}

.plugin-tool-view__card-subtitle {
  margin-top: 2px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.plugin-tool-view__card-body {
  margin: 4px 0 0;
  white-space: pre-wrap;
}

.plugin-tool-view__kv-title {
  margin-bottom: 6px;
  font-weight: 500;
}

.plugin-tool-view__kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 16px;
  margin: 0;

  dt {
    color: var(--td-text-color-secondary);
  }

  dd {
    margin: 0;
    word-break: break-word;
  }
}

.plugin-tool-view__json {
  max-height: 360px;
  margin: 0;
  padding: 8px 12px;
  overflow: auto;
  font-size: var(--app-text-sm);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-sm);
}

a {
  color: var(--td-brand-color);
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}
</style>
