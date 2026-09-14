<template>
  <div class="mcp-result">
    <div v-if="success === false" class="mcp-error" role="alert">
      <t-icon name="error-circle" />
      <pre>{{ output || $t('agentStream.mcp.failed') }}</pre>
    </div>
    <template v-else-if="discovery && isDiscoveryResult">
      <template v-if="mode !== 'describe'">
        <div class="mcp-summary">
          <span v-if="serverName" class="mcp-server">{{ serverName }}</span>
          <span>{{ $t('agentStream.mcp.showing', { count: rows.length, total: total }) }}</span>
          <span v-if="data.has_more === true"> · {{ $t('agentStream.mcp.moreAvailable') }}</span>
        </div>
        <p v-if="!rows.length" class="mcp-empty">{{ $t('agentStream.mcp.empty') }}</p>
        <div v-else class="mcp-list">
          <article v-for="(row, index) in rows" :key="`${row.name}-${index}`" class="mcp-item">
            <button
              type="button"
              class="mcp-item__head"
              :disabled="!mcpDescriptionNeedsExpand(row.description)"
              :aria-expanded="openIndex === index"
              @click="toggleRow(index, row.description)"
            >
              <span class="mcp-item__index">#{{ index + 1 }}</span>
              <code class="mcp-item__name">{{ row.name }}</code>
              <span v-if="row.serverName && row.serverName !== serverName" class="mcp-item__meta">{{ row.serverName }}</span>
              <span v-else-if="statusLabel(row.status)" class="mcp-item__meta">{{ statusLabel(row.status) }}</span>
              <t-icon
                v-if="row.description && mcpDescriptionNeedsExpand(row.description)"
                class="mcp-item__chevron"
                :name="openIndex === index ? 'chevron-up' : 'chevron-down'"
              />
            </button>
            <p
              v-if="row.description"
              :class="['mcp-item__desc', { 'is-open': openIndex === index }]"
            >{{ openIndex === index ? row.description : mcpDescriptionLead(row.description) }}</p>
          </article>
        </div>
      </template>
      <template v-else>
        <div class="mcp-define">
          <div class="mcp-define__head">
            <code v-if="typeof data.name === 'string' && data.name">{{ data.name }}</code>
            <span v-if="serverName" class="mcp-server">{{ serverName }}</span>
          </div>
          <p
            v-if="description"
            :class="['mcp-define__desc', { 'is-collapsed': !descOpen && descriptionLong }]"
          >{{ description }}</p>
          <button
            v-if="descriptionLong"
            type="button"
            class="mcp-toggle"
            :aria-expanded="descOpen"
            @click="descOpen = !descOpen"
          >{{ $t(descOpen ? 'agentStream.mcp.collapse' : 'agentStream.mcp.expand') }}</button>
          <div v-if="parameters.length" class="mcp-parameters">
            <div class="mcp-parameters__label">{{ $t('agentStream.mcp.parameters') }}</div>
            <div v-for="parameter in parameters" :key="parameter.name" class="mcp-parameter">
              <div class="mcp-parameter-title">
                <code>{{ parameter.name }}</code>
                <span v-if="parameter.type">{{ parameter.type }}</span>
                <span v-if="parameter.required" class="mcp-required">{{ $t('agentStream.mcp.required') }}</span>
              </div>
              <p v-if="parameter.description">{{ parameter.description }}</p>
            </div>
          </div>
          <details v-if="data.input_schema !== undefined" class="mcp-definition">
            <summary>{{ $t('agentStream.mcp.fullSchema') }}</summary>
            <pre>{{ JSON.stringify(data.input_schema, null, 2) }}</pre>
          </details>
        </div>
      </template>
    </template>
    <div v-else class="mcp-output">
      <div class="mcp-summary">{{ $t('agentStream.mcp.result') }}</div>
      <pre>{{ output }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  mcpDescriptionLead,
  mcpDescriptionNeedsExpand,
  mcpDiscoveryRows,
  mcpSchemaParameters,
  parseMcpDiscovery,
} from '@/utils/mcpToolDisplay'

const props = withDefaults(defineProps<{
  discovery: boolean
  output: string
  data?: Record<string, unknown>
  arguments?: Record<string, unknown>
  success?: boolean
}>(), { success: undefined })
const { t, te } = useI18n()
const data = computed(() => parseMcpDiscovery(props.output, props.data))
const mode = computed(() => data.value.mode || props.arguments?.mode || ('input_schema' in data.value ? 'describe' : ''))
const isDiscoveryResult = computed(() => Array.isArray(data.value.servers) || Array.isArray(data.value.tools) ||
  typeof data.value.total === 'number' || 'input_schema' in data.value)
const rows = computed(() => mcpDiscoveryRows({ ...data.value, mode: mode.value }))
const serverName = computed(() => {
  if (typeof data.value.server_name === 'string' && data.value.server_name) return data.value.server_name
  return rows.value.find(row => row.serverName)?.serverName || ''
})
const total = computed(() => typeof data.value.total === 'number' ? data.value.total : rows.value.length)
const parameters = computed(() => mcpSchemaParameters(data.value.input_schema))
const description = computed(() => typeof data.value.description === 'string' ? data.value.description : '')
const descriptionLong = computed(() => mcpDescriptionNeedsExpand(description.value))
const openIndex = ref(-1)
const descOpen = ref(false)

watch(() => props.output, () => {
  openIndex.value = -1
  descOpen.value = false
})

function toggleRow(index: number, description: string) {
  if (!description) return
  if (!mcpDescriptionNeedsExpand(description)) return
  openIndex.value = openIndex.value === index ? -1 : index
}

const statusLabel = (status: string) => {
  if (!status) return ''
  const key = `agentStream.mcp.status.${status}`
  return te(key) ? t(key) : status
}
</script>

<style lang="less" scoped>
.mcp-result { min-width: 0; font-size: 12px; color: var(--td-text-color-primary); }
.mcp-summary { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; margin-bottom: 8px; color: var(--td-text-color-secondary); }
.mcp-server { font-weight: 600; color: var(--td-text-color-primary); }
.mcp-empty { margin: 0; color: var(--td-text-color-placeholder); }
.mcp-list {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  background: var(--td-bg-color-container);
}
.mcp-item + .mcp-item { border-top: 1px solid var(--td-component-stroke); }
.mcp-item__head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin: 0;
  padding: 8px 10px 0;
  border: 0;
  background: transparent;
  font: inherit;
  color: inherit;
  text-align: left;
  cursor: pointer;
}
.mcp-item__head:disabled { cursor: default; }
.mcp-item__head:not(:disabled):hover .mcp-item__name { color: var(--td-brand-color); }
.mcp-item__index {
  flex-shrink: 0;
  min-width: 22px;
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}
.mcp-item__name {
  flex: 1;
  min-width: 0;
  font: 12px/1.5 var(--app-font-family-mono, monospace);
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mcp-item__meta {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
.mcp-item__chevron {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}
.mcp-item__desc {
  margin: 4px 10px 10px 40px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.mcp-item__desc.is-open {
  display: block;
  white-space: pre-wrap;
  max-height: 240px;
  overflow: auto;
}
.mcp-define__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.mcp-define__head code {
  font: 12px/1.5 var(--app-font-family-mono, monospace);
  font-weight: 600;
}
.mcp-define__head .mcp-server { font-weight: 500; color: var(--td-text-color-secondary); }
.mcp-define__desc {
  margin: 0;
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.mcp-define__desc.is-collapsed {
  white-space: normal;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.mcp-toggle {
  margin: 6px 0 0;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  font-size: 12px;
  color: var(--td-brand-color);
  cursor: pointer;
}
.mcp-parameters { margin-top: 10px; border: 1px solid var(--td-component-stroke); border-radius: 8px; overflow: hidden; }
.mcp-parameters__label {
  padding: 6px 12px;
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
}
.mcp-parameter { padding: 8px 12px; }
.mcp-parameter + .mcp-parameter { border-top: 1px solid var(--td-component-stroke); }
.mcp-parameter-title { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; overflow-wrap: anywhere; }
.mcp-parameter-title code { font-weight: 600; }
.mcp-parameter-title span { color: var(--td-text-color-secondary); }
.mcp-parameter-title .mcp-required { color: var(--td-brand-color); }
.mcp-parameter p { margin: 4px 0 0; color: var(--td-text-color-secondary); white-space: pre-wrap; overflow-wrap: anywhere; }
.mcp-definition { margin-top: 10px; }
.mcp-definition summary { cursor: pointer; color: var(--td-text-color-secondary); }
pre { margin: 8px 0 0; padding: 10px 12px; background: var(--td-bg-color-secondarycontainer); border-radius: 6px; font: 12px/1.6 var(--app-font-family-mono); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 320px; overflow: auto; }
.mcp-error { display: flex; align-items: flex-start; gap: 8px; color: var(--td-error-color); }
.mcp-error pre { margin: 0; flex: 1; min-width: 0; }
</style>
