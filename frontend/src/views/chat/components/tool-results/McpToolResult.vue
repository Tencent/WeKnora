<template>
  <div class="mcp-result">
    <div v-if="success === false" class="mcp-error" role="alert">
      <t-icon name="error-circle" />
      <pre>{{ output || $t('agentStream.mcp.failed') }}</pre>
    </div>
    <template v-else-if="discovery && isDiscoveryResult">
      <div v-if="mode !== 'describe'" class="mcp-summary">
        {{ $t('agentStream.mcp.showing', { count: rows.length, total: total }) }}
        <span v-if="data.has_more === true"> · {{ $t('agentStream.mcp.moreAvailable') }}</span>
      </div>
      <div v-for="(row, index) in rows" :key="index" class="mcp-entry">
        <ResultRow :index="index + 1" :title="row.name" :meta="statusLabel(row.status)"
          :popup-key="index" :show-popup="false" />
        <p v-if="row.description" class="mcp-description">{{ row.description }}</p>
      </div>
      <template v-if="mode === 'describe'">
        <p v-if="description" class="mcp-description">{{ description }}</p>
        <div v-if="parameters.length" class="mcp-parameters">
          <div v-for="parameter in parameters" :key="parameter.name" class="mcp-parameter">
            <div class="mcp-parameter-title">
              <code>{{ parameter.name }}</code>
              <span>{{ parameter.type }}</span>
              <span v-if="parameter.required" class="mcp-required">{{ $t('agentStream.mcp.required') }}</span>
            </div>
            <p v-if="parameter.description">{{ parameter.description }}</p>
          </div>
        </div>
        <details v-if="data.input_schema !== undefined" class="mcp-definition">
          <summary>{{ $t('agentStream.mcp.fullSchema') }}</summary>
          <pre>{{ JSON.stringify(data.input_schema, null, 2) }}</pre>
        </details>
      </template>
    </template>
    <div v-else class="mcp-output">
      <div class="mcp-summary">{{ $t('agentStream.mcp.result') }}</div>
      <pre>{{ output }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { mcpDiscoveryRows, mcpSchemaParameters, parseMcpDiscovery } from '@/utils/mcpToolDisplay'
import ResultRow from './ResultRow.vue'

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
const total = computed(() => typeof data.value.total === 'number' ? data.value.total : rows.value.length)
const parameters = computed(() => mcpSchemaParameters(data.value.input_schema))
const description = computed(() => typeof data.value.description === 'string' ? data.value.description : '')
const statusLabel = (status: string) => {
  if (!status) return ''
  const key = `agentStream.mcp.status.${status}`
  return te(key) ? t(key) : status
}
</script>

<style lang="less" scoped>
.mcp-result { min-width: 0; font-size: 12px; color: var(--td-text-color-primary); }
.mcp-summary { margin-bottom: 8px; color: var(--td-text-color-secondary); }
.mcp-entry + .mcp-entry { margin-top: 8px; }
.mcp-description { margin: 4px 8px; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--td-text-color-secondary); line-height: 1.6; }
.mcp-parameters { margin-top: 10px; border: 1px solid var(--td-component-stroke); border-radius: 6px; overflow: hidden; }
.mcp-parameter { padding: 8px 12px; }
.mcp-parameter + .mcp-parameter { border-top: 1px solid var(--td-component-stroke); }
.mcp-parameter-title { display: flex; flex-wrap: wrap; gap: 8px; overflow-wrap: anywhere; }
.mcp-parameter-title span { color: var(--td-text-color-secondary); }
.mcp-parameter-title .mcp-required { color: var(--td-brand-color); }
.mcp-parameter p { margin: 4px 0 0; color: var(--td-text-color-secondary); white-space: pre-wrap; overflow-wrap: anywhere; }
.mcp-definition { margin-top: 10px; }
.mcp-definition summary { cursor: pointer; color: var(--td-text-color-secondary); }
pre { margin: 8px 0 0; padding: 10px 12px; background: var(--td-bg-color-secondarycontainer); border-radius: 6px; font: 12px/1.6 var(--app-font-family-mono); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 320px; overflow: auto; }
.mcp-error { display: flex; align-items: flex-start; gap: 8px; color: var(--td-error-color); }
.mcp-error pre { margin: 0; flex: 1; min-width: 0; }
</style>
