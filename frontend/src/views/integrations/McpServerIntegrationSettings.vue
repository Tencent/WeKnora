<template>
  <div class="mcp-server-panel">
    <div class="channels-section">
      <div class="channels-header">
        <span class="channels-title">{{ $t('integrations.mcpserver.listTitle') }}</span>
        <span class="channels-count">{{ endpoints.length }}</span>
      </div>

      <t-loading :loading="loading" size="small" class="channels-loading-wrap">
        <div v-if="!loading && endpoints.length === 0 && !isAdmin" class="channels-empty">
          <t-empty :description="$t('integrations.mcpserver.empty')" />
        </div>

        <div v-else-if="!loading" class="channel-grid">
          <button
            v-for="ep in endpoints"
            :key="ep.id"
            type="button"
            class="channel-card channel-card--clickable"
            @click="openEdit(ep)"
          >
            <div class="channel-card__badge">
              <t-icon name="tools" size="22px" />
            </div>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <h3 class="channel-card__title">{{ ep.name }}</h3>
                <t-tag v-if="!ep.enabled" size="small" variant="light" theme="warning">
                  {{ $t('integrations.mcpserver.disabled') }}
                </t-tag>
              </div>
              <span class="channel-card__agent-name">
                {{ $t('integrations.mcpserver.cardSummary', { tools: ep.tools.length, scope: scopeLabel(ep) }) }}
              </span>
            </div>
            <div v-if="isAdmin" class="channel-card__actions" @click.stop>
              <t-dropdown
                trigger="click"
                placement="bottom-right"
                attach="body"
                :options="menuOptions(ep)"
                @click="handleMenuClick($event, ep)"
              >
                <t-button variant="text" shape="square" size="small" class="channel-card__action-btn" @click.stop>
                  <template #icon><t-icon name="ellipsis" /></template>
                </t-button>
              </t-dropdown>
              <t-popconfirm
                :content="$t('integrations.mcpserver.deleteConfirm')"
                :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }"
                placement="bottom-right"
                @confirm="() => removeEndpoint(ep)"
              >
                <t-tooltip :content="$t('common.delete')" placement="top">
                  <t-button theme="danger" shape="square" variant="text" size="small" class="channel-card__action-btn" @click.stop>
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </div>
          </button>

          <button v-if="isAdmin" type="button" class="channel-card channel-card--add" @click="openCreate">
            <span class="channel-card__badge" aria-hidden="true">
              <t-icon name="add" />
            </span>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <span class="channel-card__title">{{ $t('integrations.mcpserver.create') }}</span>
              </div>
            </div>
            <span class="channel-card__actions channel-card__actions--spacer" aria-hidden="true" />
          </button>
        </div>
      </t-loading>
    </div>

    <SettingDrawer
      v-model:visible="showDrawer"
      class="mcp-endpoint-drawer"
      :title="editing ? $t('integrations.mcpserver.editTitle') : $t('integrations.mcpserver.createTitle')"
      :description="$t('integrations.mcpserver.drawerDesc')"
      icon="tools"
      storage-key="setting-drawer:mcp-endpoint"
      width="600px"
      :confirm-loading="saving"
      :confirm-text="editing ? $t('common.save') : $t('integrations.mcpserver.create')"
      :hide-footer="!isAdmin"
      @confirm="saveForm"
      @cancel="closeDrawer"
    >
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionBasic') }}</h4>
        <div class="form-item">
          <label class="form-label required">{{ $t('integrations.mcpserver.nameLabel') }}</label>
          <t-input v-model="form.name" :placeholder="$t('integrations.mcpserver.namePlaceholder')" :maxlength="255" />
        </div>
        <div class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.descriptionLabel') }}</label>
          <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }" :placeholder="$t('integrations.mcpserver.descriptionPlaceholder')" />
        </div>
        <div class="form-item enable-row">
          <label class="form-label form-label--inline">{{ $t('integrations.mcpserver.enabledLabel') }}</label>
          <t-switch v-model="form.enabled" size="small" />
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionScope') }}</h4>
        <div class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.kbScopeLabel') }}</label>
          <t-select
            v-model="form.knowledge_base_ids"
            multiple
            filterable
            clearable
            :loading="kbLoading"
            :options="kbOptions"
            :placeholder="$t('integrations.mcpserver.kbScopePlaceholder')"
          />
          <p class="form-desc">{{ $t('integrations.mcpserver.kbScopeHint') }}</p>
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionTools') }}</h4>
        <p class="form-desc form-desc--block">{{ $t('integrations.mcpserver.toolsHint') }}</p>
        <div class="tool-group-list">
          <div v-for="group in groupedTools" :key="group.group" class="tool-group">
            <div class="tool-group__header">
              <span class="tool-group__name">{{ $t(`integrations.mcpserver.groups.${group.group}`) }}</span>
              <t-button size="small" variant="text" @click="toggleGroup(group, !groupAllSelected(group))">
                {{ groupAllSelected(group) ? $t('integrations.mcpserver.clearGroup') : $t('integrations.mcpserver.selectGroup') }}
              </t-button>
            </div>
            <div class="tool-group__items">
              <div v-for="tool in group.tools" :key="tool.name" class="tool-item" :class="{ 'tool-item--danger': tool.destructive }">
                <t-checkbox :model-value="toolSelections[tool.name] === true" @change="(v: boolean) => setToolSelected(tool.name, v)">
                  <span class="tool-item__label">
                    {{ $t(`integrations.mcpserver.tools.${tool.name}`) }}
                    <code class="tool-item__code">{{ tool.name }}</code>
                  </span>
                </t-checkbox>
                <p class="form-desc">{{ $t(`integrations.mcpserver.tools.${tool.name}Desc`) }}</p>
              </div>
            </div>
          </div>
        </div>
        <p v-if="selectedToolCount === 0" class="form-desc form-desc--error">{{ $t('integrations.mcpserver.toolsRequired') }}</p>
      </section>

      <section v-if="askSelected" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionAsk') }}</h4>
        <div class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.defaultAgentLabel') }}</label>
          <t-select
            v-model="form.default_agent_id"
            filterable
            clearable
            :loading="agentsLoading"
            :options="agentOptions"
            :placeholder="$t('integrations.mcpserver.defaultAgentPlaceholder')"
          />
          <p class="form-desc">{{ $t('integrations.mcpserver.defaultAgentHint') }}</p>
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionLimits') }}</h4>
        <div class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.rateLimitLabel') }}</label>
          <t-input-number v-model="form.rate_limit_per_minute" class="form-number" :min="1" :max="6000" theme="column" />
          <p class="form-desc">{{ $t('integrations.mcpserver.rateLimitHint') }}</p>
        </div>
      </section>

      <section v-if="editing" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionConnect') }}</h4>
        <p class="form-desc form-desc--block">{{ $t('integrations.mcpserver.connectHintExisting', { hint: editing.token_hint }) }}</p>
        <div class="code-toolbar">
          <pre class="code-toolbar__code">{{ endpointUrl(editing) }}</pre>
          <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(endpointUrl(editing))">
            <t-icon name="file-copy" size="16px" />
          </t-button>
        </div>
        <t-button size="small" variant="outline" @click="openConnectDialog(editing, '')">
          {{ $t('integrations.mcpserver.showSnippets') }}
        </t-button>
      </section>
    </SettingDrawer>

    <t-dialog
      v-model:visible="connectVisible"
      :header="connectToken ? $t('integrations.mcpserver.tokenDialogTitle') : $t('integrations.mcpserver.connectDialogTitle')"
      width="640px"
      :footer="false"
      :close-on-overlay-click="!connectToken"
    >
      <div class="connect-dialog">
        <t-alert v-if="connectToken" theme="warning" :message="$t('integrations.mcpserver.tokenOnce')" class="connect-dialog__alert" />
        <p v-else class="form-desc form-desc--block">{{ $t('integrations.mcpserver.connectPlaceholderHint') }}</p>

        <div v-if="connectToken" class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.tokenLabel') }}</label>
          <div class="code-toolbar">
            <pre class="code-toolbar__code">{{ connectToken }}</pre>
            <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(connectToken)">
              <t-icon name="file-copy" size="16px" />
            </t-button>
          </div>
        </div>

        <div class="form-item">
          <label class="form-label">{{ $t('integrations.mcpserver.urlLabel') }}</label>
          <div class="code-toolbar">
            <pre class="code-toolbar__code">{{ connectUrl }}</pre>
            <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(connectUrl)">
              <t-icon name="file-copy" size="16px" />
            </t-button>
          </div>
        </div>

        <div v-for="snippet in connectSnippets" :key="snippet.key" class="form-item">
          <label class="form-label">{{ $t(`integrations.mcpserver.snippet.${snippet.key}Title`) }}</label>
          <p class="form-desc form-desc--block">{{ $t(`integrations.mcpserver.snippet.${snippet.key}Desc`) }}</p>
          <div class="code-toolbar">
            <pre class="code-toolbar__code">{{ snippet.text }}</pre>
            <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(snippet.text)">
              <t-icon name="file-copy" size="16px" />
            </t-button>
          </div>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import { copyWithToast } from '@/utils/clipboard'
import { useApiBaseUrlDisplay } from '@/composables/useApiBaseUrlDisplay'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { listAgents, type CustomAgent } from '@/api/agent'
import { listKnowledgeBases } from '@/api/knowledge-base'
import {
  createMcpEndpoint,
  deleteMcpEndpoint,
  getMcpEndpointToolCatalog,
  listMcpEndpoints,
  rotateMcpEndpointToken,
  updateMcpEndpoint,
  type McpEndpoint,
  type McpEndpointPayload,
  type McpEndpointToolCatalog,
} from '@/api/mcp-endpoint'
import {
  buildClaudeCodeCommand,
  buildHttpClientSnippet,
  buildMcpEndpointUrl,
  buildStdioBridgeSnippet,
  groupTools,
} from './mcpServerIntegration'

const { t } = useI18n()
const authStore = useAuthStore()
const { apiBaseUrlDisplay } = useApiBaseUrlDisplay()

const isAdmin = computed(() => authStore.hasRole('admin'))

const loading = ref(false)
const endpoints = ref<McpEndpoint[]>([])
const catalog = ref<McpEndpointToolCatalog>({ groups: [], tools: [], default_tools: [] })

const kbLoading = ref(false)
const knowledgeBases = ref<{ id: string; name: string }[]>([])
const kbOptions = computed(() => knowledgeBases.value.map((kb) => ({ label: kb.name || kb.id, value: kb.id })))
const kbNameById = computed(() => Object.fromEntries(knowledgeBases.value.map((kb) => [kb.id, kb.name || kb.id])))

const agentsLoading = ref(false)
const agents = ref<CustomAgent[]>([])
const agentOptions = computed(() => agents.value.map((a) => ({ label: a.name, value: a.id })))

const showDrawer = ref(false)
const saving = ref(false)
const editing = ref<McpEndpoint | null>(null)
const form = reactive({
  name: '',
  description: '',
  enabled: true,
  knowledge_base_ids: [] as string[],
  default_agent_id: '',
  rate_limit_per_minute: 60,
})
const toolSelections = reactive<Record<string, boolean>>({})

const groupedTools = computed(() => groupTools(catalog.value.groups, catalog.value.tools))
const selectedToolNames = computed(() => catalog.value.tools.filter((tl) => toolSelections[tl.name]).map((tl) => tl.name))
const selectedToolCount = computed(() => selectedToolNames.value.length)
const askSelected = computed(() => toolSelections.ask === true)

const connectVisible = ref(false)
const connectToken = ref('')
const connectEndpoint = ref<McpEndpoint | null>(null)
const connectUrl = computed(() => (connectEndpoint.value ? endpointUrl(connectEndpoint.value) : ''))
const connectSnippets = computed(() => {
  const ep = connectEndpoint.value
  if (!ep) return []
  const url = endpointUrl(ep)
  return [
    { key: 'http', text: buildHttpClientSnippet(ep.name, url, connectToken.value, ep.id) },
    { key: 'claudeCode', text: buildClaudeCodeCommand(ep.name, url, connectToken.value, ep.id) },
    { key: 'stdio', text: buildStdioBridgeSnippet(ep.name, url, connectToken.value, ep.id) },
  ]
})

function endpointUrl(ep: McpEndpoint): string {
  return buildMcpEndpointUrl(apiBaseUrlDisplay.value, ep.path)
}

function scopeLabel(ep: McpEndpoint): string {
  if (!ep.knowledge_base_ids?.length) return t('integrations.mcpserver.scopeAll')
  return t('integrations.mcpserver.scopeCount', { count: ep.knowledge_base_ids.length })
}

function setToolSelected(name: string, selected: boolean) {
  toolSelections[name] = selected
}

function groupAllSelected(group: { tools: { name: string }[] }) {
  return group.tools.every((tl) => toolSelections[tl.name])
}

function toggleGroup(group: { tools: { name: string }[] }, selected: boolean) {
  for (const tl of group.tools) toolSelections[tl.name] = selected
}

function resetForm(ep: McpEndpoint | null) {
  form.name = ep?.name ?? ''
  form.description = ep?.description ?? ''
  form.enabled = ep?.enabled ?? true
  form.knowledge_base_ids = [...(ep?.knowledge_base_ids ?? [])]
  form.default_agent_id = ep?.default_agent_id ?? ''
  form.rate_limit_per_minute = ep?.rate_limit_per_minute || 60
  const selected = new Set(ep ? ep.tools : catalog.value.default_tools)
  for (const key of Object.keys(toolSelections)) delete toolSelections[key]
  for (const tl of catalog.value.tools) toolSelections[tl.name] = selected.has(tl.name)
}

async function load() {
  loading.value = true
  try {
    const [epRes, catRes] = await Promise.all([listMcpEndpoints(), getMcpEndpointToolCatalog()])
    endpoints.value = epRes?.data ?? []
    catalog.value = catRes?.data ?? { groups: [], tools: [], default_tools: [] }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.mcpserver.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
  kbLoading.value = true
  agentsLoading.value = true
  try {
    const [kbRes, agentRes] = await Promise.all([
      listKnowledgeBases({ creator: 'all' }).catch(() => null),
      listAgents().catch(() => null),
    ])
    const kbRows = ((kbRes as any)?.data ?? []) as Array<{ id: string | number; name?: string }>
    knowledgeBases.value = kbRows.map((kb) => ({ id: String(kb.id), name: kb.name || String(kb.id) }))
    agents.value = ((agentRes as any)?.data ?? []) as CustomAgent[]
  } finally {
    kbLoading.value = false
    agentsLoading.value = false
  }
}

function openCreate() {
  editing.value = null
  resetForm(null)
  showDrawer.value = true
  void loadOptions()
}

function openEdit(ep: McpEndpoint) {
  editing.value = ep
  resetForm(ep)
  showDrawer.value = true
  void loadOptions()
}

function closeDrawer() {
  showDrawer.value = false
}

function buildPayload(): McpEndpointPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    enabled: form.enabled,
    knowledge_base_ids: [...form.knowledge_base_ids],
    tools: [...selectedToolNames.value],
    default_agent_id: askSelected.value ? form.default_agent_id : '',
    rate_limit_per_minute: form.rate_limit_per_minute,
  }
}

async function saveForm() {
  if (!form.name.trim()) {
    MessagePlugin.warning(t('integrations.mcpserver.nameRequired'))
    return
  }
  if (selectedToolCount.value === 0) {
    MessagePlugin.warning(t('integrations.mcpserver.toolsRequired'))
    return
  }
  saving.value = true
  try {
    const payload = buildPayload()
    if (editing.value) {
      await updateMcpEndpoint(editing.value.id, payload)
      MessagePlugin.success(t('integrations.mcpserver.updated'))
      showDrawer.value = false
      await load()
    } else {
      const res = await createMcpEndpoint(payload)
      MessagePlugin.success(t('integrations.mcpserver.created'))
      showDrawer.value = false
      await load()
      if (res?.data) openConnectDialog(res.data, res.data.token || '')
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.mcpserver.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function removeEndpoint(ep: McpEndpoint) {
  try {
    await deleteMcpEndpoint(ep.id)
    MessagePlugin.success(t('integrations.mcpserver.deleted'))
    await load()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.mcpserver.deleteFailed'))
  }
}

async function rotateToken(ep: McpEndpoint) {
  try {
    const res = await rotateMcpEndpointToken(ep.id)
    MessagePlugin.success(t('integrations.mcpserver.rotated'))
    await load()
    if (res?.data) openConnectDialog(res.data, res.data.token || '')
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.mcpserver.rotateFailed'))
  }
}

async function toggleEnabled(ep: McpEndpoint) {
  try {
    await updateMcpEndpoint(ep.id, { enabled: !ep.enabled })
    MessagePlugin.success(ep.enabled ? t('integrations.mcpserver.disabledToast') : t('integrations.mcpserver.enabledToast'))
    await load()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.mcpserver.saveFailed'))
  }
}

function openConnectDialog(ep: McpEndpoint, token: string) {
  connectEndpoint.value = ep
  connectToken.value = token
  connectVisible.value = true
}

function menuOptions(ep: McpEndpoint) {
  return [
    { content: t('integrations.mcpserver.menuConnect'), value: 'connect' },
    { content: ep.enabled ? t('integrations.mcpserver.menuDisable') : t('integrations.mcpserver.menuEnable'), value: 'toggle' },
    { content: t('integrations.mcpserver.menuRotate'), value: 'rotate' },
  ]
}

function handleMenuClick(option: { value?: string | number }, ep: McpEndpoint) {
  switch (option?.value) {
    case 'connect':
      openConnectDialog(ep, '')
      break
    case 'toggle':
      void toggleEnabled(ep)
      break
    case 'rotate':
      void rotateToken(ep)
      break
  }
}

const copyText = (text: string) => copyWithToast(text, 'integrations.mcpserver.copied')

onMounted(() => {
  void load()
})
</script>

<style scoped lang="less">
@import '../../components/css/channel-panel-list.less';

.mcp-server-panel {
  display: flex;
  flex-direction: column;
}

.form-item {
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  &--inline {
    margin-bottom: 0;
  }

  &.required::after {
    content: ' *';
    color: var(--td-error-color);
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-placeholder);

  &--block {
    margin: -2px 0 0;
    color: var(--td-text-color-secondary);
  }

  &--error {
    color: var(--td-error-color);
  }
}

.form-number {
  width: 100%;
  max-width: 200px;
}

.enable-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.tool-group-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tool-group {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 10px 12px;
}

.tool-group__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.tool-group__name {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.tool-group__items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tool-item {
  padding-left: 2px;

  .form-desc {
    margin-left: 24px;
  }

  &--danger .tool-item__label {
    color: var(--td-error-color);
  }
}

.tool-item__code {
  margin-left: 6px;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 5px;
  border-radius: 4px;
}

.code-toolbar {
  position: relative;
  margin: 6px 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.code-toolbar__code {
  margin: 0;
  padding: 10px 40px 10px 12px;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--td-text-color-primary);
}

.code-toolbar__copy {
  position: absolute;
  top: 6px;
  right: 6px;
}

.connect-dialog {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.connect-dialog__alert {
  margin-bottom: 4px;
}
</style>
