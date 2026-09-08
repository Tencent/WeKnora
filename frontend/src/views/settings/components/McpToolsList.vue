<template>
  <div class="tools-directory">
    <t-input v-if="tools.length > pageSize" v-model="query" clearable :placeholder="t('mcpMetadata.searchTools')">
      <template #prefix-icon><t-icon name="search" /></template>
    </t-input>
    <t-alert v-if="policyError" theme="error" :message="policyError">
      <template #operation><t-button variant="text" size="small" @click="loadPolicies">{{ t('mcpMetadata.retry') }}</t-button></template>
    </t-alert>
    <div v-if="visibleTools.length" class="tools-list">
      <article v-for="tool in visibleTools" :key="tool.name" class="tool-row">
        <button type="button" class="tool-heading" :aria-expanded="expanded === tool.name" @click="expanded = expanded === tool.name ? '' : tool.name">
          <span class="tool-name">{{ tool.name }}</span>
          <span class="tool-expand"><span>{{ t(expanded === tool.name ? 'mcpMetadata.hideParameters' : 'mcpMetadata.parameters') }}</span><t-icon :name="expanded === tool.name ? 'chevron-up' : 'chevron-down'" /></span>
        </button>
        <p v-if="tool.description" :class="['tool-description', { 'is-collapsed': expanded !== tool.name }]">{{ tool.description }}</p>
        <div v-if="serviceId" class="tool-controls">
          <label class="tool-control"><span>{{ t('mcpMetadata.enabled') }}</span>
            <t-switch :value="policy(tool.name).enabled" size="small" :disabled="loading || !!policyError || !!busy.get(tool.name)" :loading="busy.get(tool.name) === 'enabled'"
              :aria-label="`${tool.name} ${t('mcpMetadata.enabled')}`" @change="(value: boolean) => savePolicy(tool.name, 'enabled', value)" />
          </label>
          <label class="tool-control"><span>{{ t('mcpMetadata.approval') }}</span>
            <t-switch :value="policy(tool.name).require_approval" size="small" :disabled="loading || !!policyError || !!busy.get(tool.name)" :loading="busy.get(tool.name) === 'require_approval'"
              :aria-label="`${tool.name} ${t('mcpMetadata.approval')}`" @change="(value: boolean) => savePolicy(tool.name, 'require_approval', value)" />
          </label>
        </div>
        <div v-if="expanded === tool.name" class="tool-schema">
          <pre>{{ JSON.stringify(tool.inputSchema, null, 2) }}</pre>
        </div>
      </article>
    </div>
    <t-empty v-else :description="t('mcpMetadata.noTools')" size="small" />
    <div v-if="filtered.length > pageSize" class="tools-pagination">
      <t-button variant="text" size="small" :disabled="page === 1" @click="page--">{{ t('mcpMetadata.previous') }}</t-button>
      <span>{{ page }} / {{ Math.ceil(filtered.length / pageSize) }}</span>
      <t-button variant="text" size="small" :disabled="page * pageSize >= filtered.length" @click="page++">{{ t('mcpMetadata.next') }}</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getMCPToolApprovals, setMCPToolApproval, setMCPToolEnabled, type MCPTool, type MCPToolApprovalRow } from '@/api/mcp-service'

const props = defineProps<{ tools: MCPTool[]; serviceId?: string }>()
const emit = defineEmits<{ (e: 'busy', value: boolean): void }>()
const { t } = useI18n()
const pageSize = 20
const page = ref(1)
const query = ref('')
const expanded = ref('')
const loading = ref(false)
const policyError = ref('')
const policies = ref(new Map<string, Pick<MCPToolApprovalRow, 'enabled' | 'require_approval'>>())
const busy = ref(new Map<string, string>())
let generation = 0
const filtered = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  return needle ? props.tools.filter(tool => `${tool.name} ${tool.description}`.toLocaleLowerCase().includes(needle)) : props.tools
})
const visibleTools = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch([query, () => props.tools], () => { page.value = 1; expanded.value = '' })
const policy = (name: string) => policies.value.get(name) ?? { enabled: true, require_approval: false }

async function loadPolicies() {
  const current = ++generation
  policies.value = new Map()
  policyError.value = ''
  busy.value = new Map()
  emit('busy', false)
  if (!props.serviceId) { loading.value = false; return }
  loading.value = true
  try {
    const rows = await getMCPToolApprovals(props.serviceId)
    if (current === generation) policies.value = new Map(rows.map(row => [row.tool_name, row]))
  } catch {
    if (current === generation) policyError.value = t('mcpMetadata.policyLoadFailed')
  } finally { if (current === generation) loading.value = false }
}
watch(() => props.serviceId, loadPolicies, { immediate: true })

async function savePolicy(name: string, field: 'enabled' | 'require_approval', value: boolean) {
  if (!props.serviceId || busy.value.has(name) || loading.value || policyError.value) return
  const current = generation
  busy.value.set(name, field)
  emit('busy', true)
  try {
    if (field === 'enabled') await setMCPToolEnabled(props.serviceId, name, value)
    else await setMCPToolApproval(props.serviceId, name, value)
    if (current === generation) policies.value.set(name, { ...policy(name), [field]: value })
  } catch { if (current === generation) MessagePlugin.error(t('mcpMetadata.policySaveFailed')) }
  finally {
    if (current === generation) { busy.value.delete(name); emit('busy', busy.value.size > 0) }
  }
}
onBeforeUnmount(() => { generation++; emit('busy', false) })
</script>

<style scoped lang="less">
.tools-directory { display: flex; flex-direction: column; gap: 12px; margin-top: 12px; min-width: 0; }
.tools-list { border-top: 1px solid var(--td-component-stroke); }
.tool-row { padding: 14px 0; min-width: 0; border-bottom: 1px solid var(--td-component-stroke); }
.tool-row:last-child { border-bottom: 0; padding-bottom: 0; }
.tool-heading { display: flex; width: 100%; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 0; border: 0; background: transparent; font: inherit; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.tool-heading:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 4px; border-radius: 3px; }
.tool-name { min-width: 0; font-size: 13px; font-weight: 600; line-height: 1.6; overflow-wrap: anywhere; }
.tool-expand { flex-shrink: 0; display: flex; align-items: center; gap: 4px; font-size: 12px; line-height: 1.7; color: var(--td-text-color-placeholder); }
.tool-heading:hover .tool-expand { color: var(--td-brand-color); }
.tool-description { margin: 6px 0 0; font-size: 12px; line-height: 1.65; color: var(--td-text-color-secondary); overflow-wrap: anywhere; white-space: pre-wrap; }
.tool-description.is-collapsed { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.tool-controls { display: flex; align-items: center; gap: 24px; flex-wrap: wrap; margin-top: 10px; }
.tool-control { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; line-height: 20px; color: var(--td-text-color-secondary); cursor: pointer; }
.tool-schema { margin-top: 12px; padding: 10px 12px; border-radius: 6px; background: var(--td-bg-color-secondarycontainer); }
.tool-schema pre { margin: 0; max-height: 300px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.65 var(--app-font-family-mono, monospace); color: var(--td-text-color-secondary); }
.tools-pagination { display: flex; align-items: center; justify-content: flex-end; gap: 8px; font-size: 12px; color: var(--td-text-color-secondary); }
</style>
