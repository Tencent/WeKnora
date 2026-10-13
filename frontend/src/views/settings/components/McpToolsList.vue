<template>
  <div class="tools-directory">
    <t-input v-if="tools.length > pageSize" v-model="query" clearable :placeholder="t('mcpMetadata.searchTools')">
      <template #prefix-icon><t-icon name="search" /></template>
    </t-input>
    <t-alert v-if="policyError" theme="error" :message="policyError">
      <template #operation><t-button variant="text" size="small" @click="loadPolicies">{{ t('mcpMetadata.retry') }}</t-button></template>
    </t-alert>
    <div v-if="visibleTools.length" class="tools-list" :class="{ 'has-controls': !!serviceId }">
      <div class="tools-list__header">
        <span class="tools-list__header-tool">
          {{ t('mcpMetadata.columnTool') }}
          <t-tooltip v-if="serviceId" :content="t('mcpMetadata.policyHint')" placement="top"
            :overlay-inner-style="{ maxWidth: '280px', whiteSpace: 'normal' }">
            <t-icon name="help-circle" size="14px" class="tools-list__help" />
          </t-tooltip>
        </span>
        <template v-if="serviceId">
          <span class="tools-list__header-col">{{ t('mcpMetadata.columnEnabled') }}</span>
          <span class="tools-list__header-col">{{ t('mcpMetadata.columnApproval') }}</span>
        </template>
      </div>
      <article v-for="tool in visibleTools" :key="tool.name" class="tool-row">
        <div class="tool-main">
          <div class="tool-heading">
            <span class="tool-name">{{ tool.name }}</span>
            <t-popup
              :visible="openTool === tool.name"
              trigger="click"
              placement="bottom-left"
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
        </div>
        <template v-if="serviceId">
          <div class="tool-cell">
            <t-switch :value="policy(tool.name).enabled" size="small" :disabled="loading || !!policyError || !!busy.get(tool.name)" :loading="busy.get(tool.name) === 'enabled'"
              :aria-label="`${tool.name} ${t('mcpMetadata.enabled')}`" @change="(value: boolean) => savePolicy(tool.name, 'enabled', value)" />
          </div>
          <div class="tool-cell">
            <t-switch :value="policy(tool.name).require_approval" size="small" :disabled="loading || !!policyError || !!busy.get(tool.name)" :loading="busy.get(tool.name) === 'require_approval'"
              :aria-label="`${tool.name} ${t('mcpMetadata.approval')}`" @change="(value: boolean) => savePolicy(tool.name, 'require_approval', value)" />
          </div>
        </template>
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
.tools-list {
  --tool-columns: minmax(0, 1fr);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  overflow: hidden;
}
.tools-list.has-controls { --tool-columns: minmax(0, 1fr) 64px 64px; }
.tools-list__header,
.tool-row {
  display: grid;
  grid-template-columns: var(--tool-columns);
  column-gap: 8px;
  padding: 0 14px;
}
.tools-list__header {
  align-items: center;
  height: 34px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
}
.tools-list__header-tool { display: inline-flex; align-items: center; gap: 4px; }
.tools-list__header-col { text-align: center; white-space: nowrap; }
.tools-list__help { color: var(--td-text-color-placeholder); cursor: help; }
.tool-row { padding-top: 12px; padding-bottom: 12px; min-width: 0; border-bottom: 1px solid var(--td-component-stroke); }
.tool-row:last-child { border-bottom: 0; }
.tool-main { min-width: 0; }
.tool-cell { display: flex; justify-content: center; align-items: flex-start; padding-top: 2px; }
.tool-heading { display: flex; min-width: 0; align-items: baseline; gap: 8px; }
.tool-name { min-width: 0; font-size: var(--app-text-md); font-weight: 600; line-height: 1.6; overflow-wrap: anywhere; }
.tool-details {
  flex-shrink: 0;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 1.7;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
}
.tool-details:hover,
.tool-details.is-open,
.tool-details:focus-visible { color: var(--td-brand-color); }
.tool-description {
  margin: 2px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
  white-space: normal;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.tools-pagination { display: flex; align-items: center; justify-content: flex-end; gap: 8px; font-size: var(--app-text-sm); color: var(--td-text-color-secondary); }
</style>

<!-- t-popup attaches to body; z-index must sit above SettingDrawer (2500). -->
<style lang="less">
.mcp-tool-detail-popup {
  z-index: 3100 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 380px;
    max-width: calc(100vw - 24px);
    border-radius: var(--app-radius-xl) !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
    overflow: hidden;
  }
}

:root[theme-mode='dark'] .mcp-tool-detail-popup .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
}

.tool-detail__tabs {
  display: inline-flex;
  gap: 2px;
  margin: 10px 12px 0;
  padding: 2px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.tool-detail__tab {
  height: 24px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 24px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;
}

.tool-detail__tab:hover {
  color: var(--td-text-color-primary);
}

.tool-detail__tab.is-active {
  color: var(--td-text-color-primary);
  font-weight: 500;
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
}

.tool-detail__body {
  padding: 10px 14px 14px;
  max-height: min(50vh, 360px);
  overflow: auto;
}

.tool-detail__desc {
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 1.65;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.tool-detail__empty {
  margin: 0;
  font-size: var(--app-text-sm);
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
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.tool-detail__param-title .is-required {
  color: var(--td-brand-color);
}

.tool-detail__param p {
  margin: 4px 0 0;
  font-size: var(--app-text-sm);
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
