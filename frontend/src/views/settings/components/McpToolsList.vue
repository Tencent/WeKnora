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
        <div class="tool-heading">
          <span class="tool-name">{{ tool.name }}</span>
          <t-popup
            :visible="openTool === tool.name"
            trigger="click"
            placement="bottom-right"
            attach="body"
            destroy-on-close
            overlay-class-name="mcp-tool-detail-popup"
            :overlay-inner-style="{ padding: 0 }"
            @visible-change="(visible: boolean) => setOpenTool(tool.name, visible)"
          >
            <button
              type="button"
              class="tool-details"
              :class="{ 'is-open': openTool === tool.name }"
              :aria-expanded="openTool === tool.name"
            >
              {{ t('mcpMetadata.details') }}
            </button>
            <template #content>
              <div class="tool-detail" @click.stop>
                <div class="tool-detail__tabs" role="tablist">
                  <button
                    v-for="tab in detailTabs"
                    :key="tab"
                    type="button"
                    class="tool-detail__tab"
                    :class="{ 'is-active': schemaView === tab }"
                    role="tab"
                    :aria-selected="schemaView === tab"
                    @click="schemaView = tab"
                  >{{ t(detailTabLabel[tab]) }}</button>
                </div>
                <div class="tool-detail__body">
                  <p v-if="schemaView === 'description' && tool.description" class="tool-detail__desc">{{ tool.description }}</p>
                  <p v-else-if="schemaView === 'description'" class="tool-detail__empty">{{ t('mcpMetadata.noDescription') }}</p>
                  <template v-else-if="schemaView === 'parameters'">
                    <div v-if="parametersOf(tool).length" class="tool-detail__params">
                      <div v-for="parameter in parametersOf(tool)" :key="parameter.name" class="tool-detail__param">
                        <div class="tool-detail__param-title">
                          <code>{{ parameter.name }}</code>
                          <span v-if="parameter.type">{{ parameter.type }}</span>
                          <span v-if="parameter.required" class="is-required">{{ t('mcpMetadata.required') }}</span>
                        </div>
                        <p v-if="parameter.description">{{ parameter.description }}</p>
                      </div>
                    </div>
                    <p v-else class="tool-detail__empty">{{ t('mcpMetadata.noParameters') }}</p>
                  </template>
                  <pre v-else-if="tool.inputSchema" class="tool-detail__json">{{ JSON.stringify(tool.inputSchema, null, 2) }}</pre>
                  <p v-else class="tool-detail__empty">{{ t('mcpMetadata.noParameters') }}</p>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
        <p v-if="tool.description" class="tool-description">{{ tool.description }}</p>
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
import { mcpSchemaParameters } from '@/utils/mcpToolDisplay'

const props = defineProps<{ tools: MCPTool[]; serviceId?: string }>()
const emit = defineEmits<{ (e: 'busy', value: boolean): void }>()
const { t } = useI18n()
const pageSize = 20
const page = ref(1)
const query = ref('')
const openTool = ref('')
const schemaView = ref<'description' | 'parameters' | 'schema'>('description')
const detailTabs = ['description', 'parameters', 'schema'] as const
const detailTabLabel = {
  description: 'mcpMetadata.description',
  parameters: 'mcpMetadata.parameters',
  schema: 'mcpMetadata.fullSchema',
} as const
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
watch([query, () => props.tools], () => { page.value = 1; openTool.value = '' })
watch(page, () => { openTool.value = '' })
const policy = (name: string) => policies.value.get(name) ?? { enabled: true, require_approval: false }
const parametersOf = (tool: MCPTool) => mcpSchemaParameters(tool.inputSchema)

function setOpenTool(name: string, visible: boolean) {
  if (visible) {
    openTool.value = name
    schemaView.value = 'description'
    return
  }
  if (openTool.value === name) openTool.value = ''
}

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
.tools-directory { display: flex; flex-direction: column; gap: 12px; margin-top: 0; min-width: 0; }
.tools-list { border-top: 1px solid var(--td-component-stroke); }
.tool-row { padding: 14px 0; min-width: 0; border-bottom: 1px solid var(--td-component-stroke); }
.tool-row:last-child { border-bottom: 0; padding-bottom: 0; }
.tool-heading { display: flex; width: 100%; align-items: flex-start; justify-content: space-between; gap: 16px; }
.tool-name { min-width: 0; font-size: 13px; font-weight: 600; line-height: 1.6; overflow-wrap: anywhere; }
.tool-details {
  flex-shrink: 0;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  font-size: 12px;
  line-height: 1.7;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
}
.tool-details:hover,
.tool-details.is-open,
.tool-details:focus-visible { color: var(--td-brand-color); }
.tool-description {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
  white-space: normal;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.tool-controls { display: flex; align-items: center; gap: 24px; flex-wrap: wrap; margin-top: 10px; }
.tool-control { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; line-height: 20px; color: var(--td-text-color-secondary); cursor: pointer; }
.tools-pagination { display: flex; align-items: center; justify-content: flex-end; gap: 8px; font-size: 12px; color: var(--td-text-color-secondary); }
</style>

<!-- t-popup attaches to body; z-index must sit above SettingDrawer (2500). -->
<style lang="less">
.mcp-tool-detail-popup {
  z-index: 3100 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 400px;
    max-width: calc(100vw - 24px);
    border-radius: 8px !important;
    background: var(--td-bg-color-container) !important;
    border: 1px solid var(--td-component-stroke) !important;
    box-shadow: var(--td-shadow-2, 0 3px 14px 2px rgba(0, 0, 0, 0.05)) !important;
  }
}

.tool-detail__tabs {
  display: flex;
  gap: 16px;
  padding: 0 14px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.tool-detail__tab {
  margin-bottom: -1px;
  padding: 10px 0 8px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  font: inherit;
  font-size: 13px;
  line-height: 1.2;
  color: var(--td-text-color-secondary);
  cursor: pointer;
}

.tool-detail__tab:hover {
  color: var(--td-text-color-primary);
}

.tool-detail__tab.is-active {
  color: var(--td-text-color-primary);
  font-weight: 500;
  border-bottom-color: var(--td-brand-color);
}

.tool-detail__body {
  padding: 12px 14px 14px;
  max-height: min(50vh, 360px);
  overflow: auto;
}

.tool-detail__desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.tool-detail__empty {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.tool-detail__param {
  padding: 8px 0;
}

.tool-detail__param + .tool-detail__param {
  border-top: 1px solid var(--td-component-stroke);
}

.tool-detail__param-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  overflow-wrap: anywhere;
}

.tool-detail__param-title code {
  font: 12px/1.5 var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-weight: 600;
}

.tool-detail__param-title span {
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.tool-detail__param-title .is-required {
  color: var(--td-brand-color);
}

.tool-detail__param p {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.tool-detail__json {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 12px/1.55 var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  color: var(--td-text-color-secondary);
}
</style>
