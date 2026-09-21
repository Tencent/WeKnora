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
      :description="drawerStepDescription"
      icon="tools"
      storage-key="setting-drawer:mcp-endpoint"
      width="600px"
      :confirm-loading="saving"
      :confirm-text="drawerConfirmText"
      :hide-footer="!isAdmin"
      :close-on-overlay-click="!freshToken"
      @confirm="handleDrawerConfirm"
      @cancel="closeDrawer"
    >
      <template v-if="wizardStep > 0 && editing" #footer-left>
        <t-button variant="outline" @click="goToWizardStep(0)">
          {{ $t('common.back') }}
        </t-button>
      </template>

      <div class="im-steps">
        <button
          v-for="(title, i) in stepTitles"
          :key="i"
          type="button"
          :class="['im-step', { active: wizardStep === i, done: wizardStep > i }]"
          :disabled="!editing && i > 0"
          @click="goToWizardStep(i)"
        >
          <span class="im-step-num">
            <t-icon v-if="wizardStep > i" name="check" class="im-step-check" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="im-step-title">{{ title }}</span>
        </button>
      </div>

      <!-- Step 1: configuration -->
      <div v-if="wizardStep === 0" class="im-step-body">
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
      </div>

      <!-- Step 2: connection (token shown once right after create / rotate) -->
      <div v-else-if="editing" class="im-step-body">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('integrations.mcpserver.sectionConnect') }}</h4>
          <t-alert v-if="freshToken" theme="warning" :message="$t('integrations.mcpserver.tokenOnce')" class="connect-alert" />
          <p v-else class="form-desc form-desc--block">
            {{ $t('integrations.mcpserver.connectHintExisting', { hint: editing.token_hint }) }}
          </p>

          <div v-if="freshToken" class="form-item">
            <label class="form-label">{{ $t('integrations.mcpserver.tokenLabel') }}</label>
            <div class="code-toolbar">
              <pre class="code-toolbar__code">{{ freshToken }}</pre>
              <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(freshToken)">
                <t-icon name="file-copy" size="16px" />
              </t-button>
            </div>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('integrations.mcpserver.urlLabel') }}</label>
            <div class="code-toolbar">
              <pre class="code-toolbar__code">{{ endpointUrl(editing) }}</pre>
              <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(endpointUrl(editing))">
                <t-icon name="file-copy" size="16px" />
              </t-button>
            </div>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('integrations.mcpserver.snippetsLabel') }}</label>
            <p v-if="!freshToken" class="form-desc form-desc--block">{{ $t('integrations.mcpserver.connectPlaceholderHint') }}</p>
            <t-tabs v-model="snippetTab" size="medium" class="snippet-tabs">
              <t-tab-panel v-for="snippet in connectSnippets" :key="snippet.key" :value="snippet.key" :label="$t(`integrations.mcpserver.snippet.${snippet.key}Title`)">
                <p class="form-desc form-desc--block">{{ $t(`integrations.mcpserver.snippet.${snippet.key}Desc`) }}</p>
                <div class="code-toolbar">
                  <pre class="code-toolbar__code">{{ snippet.text }}</pre>
                  <t-button class="code-toolbar__copy" size="small" variant="text" shape="square" :title="$t('common.copy')" @click="copyText(snippet.text)">
                    <t-icon name="file-copy" size="16px" />
                  </t-button>
                </div>
              </t-tab-panel>
            </t-tabs>
          </div>
        </section>
      </div>
    </SettingDrawer>
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
const wizardStep = ref(0)
const snippetTab = ref('http')
// Plaintext token from the create / rotate response; shown once on step 2.
const freshToken = ref('')

const stepTitles = computed(() => [
  t('integrations.mcpserver.stepConfig'),
  t('integrations.mcpserver.stepConnect'),
])
const drawerStepDescription = computed(() => {
  if (wizardStep.value === 0) return t('integrations.mcpserver.drawerDesc')
  return freshToken.value
    ? t('integrations.mcpserver.tokenDialogTitle')
    : t('integrations.mcpserver.connectDialogTitle')
})
const drawerConfirmText = computed(() => {
  if (wizardStep.value > 0) return t('common.finish')
  return editing.value ? t('common.save') : t('integrations.mcpserver.create')
})
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

const connectSnippets = computed(() => {
  const ep = editing.value
  if (!ep) return []
  const url = endpointUrl(ep)
  return [
    { key: 'http', text: buildHttpClientSnippet(ep.name, url, freshToken.value, ep.id) },
    { key: 'claudeCode', text: buildClaudeCodeCommand(ep.name, url, freshToken.value, ep.id) },
    { key: 'stdio', text: buildStdioBridgeSnippet(ep.name, url, freshToken.value, ep.id) },
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
  freshToken.value = ''
  wizardStep.value = 0
  resetForm(null)
  showDrawer.value = true
  void loadOptions()
}

function openEdit(ep: McpEndpoint, step: 0 | 1 = 0, token = '') {
  editing.value = ep
  freshToken.value = token
  wizardStep.value = step
  snippetTab.value = 'http'
  resetForm(ep)
  showDrawer.value = true
  if (step === 0) void loadOptions()
}

function goToWizardStep(step: number) {
  if (step < 0 || step >= stepTitles.value.length) return
  if (step > 0 && !editing.value) return
  wizardStep.value = step
}

function closeDrawer() {
  showDrawer.value = false
  freshToken.value = ''
}

function handleDrawerConfirm() {
  if (wizardStep.value > 0) {
    closeDrawer()
    return
  }
  void saveForm()
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
      await load()
      // Stay in the drawer and move to the connection step so the one-time
      // token is read in place instead of in a second, taller dialog.
      if (res?.data) openEdit(res.data, 1, res.data.token || '')
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
    if (res?.data) openEdit(res.data, 1, res.data.token || '')
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
      openEdit(ep, 1)
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
  font-size: var(--app-text-md);
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
  font-size: var(--app-text-sm);
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
  border-radius: var(--app-radius-md);
  padding: 10px 12px;
}

.tool-group__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.tool-group__name {
  font-size: var(--app-text-md);
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
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 5px;
  border-radius: var(--app-radius-xs);
}

.code-toolbar {
  position: relative;
  margin: 6px 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.code-toolbar__code {
  margin: 0;
  padding: 10px 40px 10px 12px;
  font-family: var(--td-font-family-mono);
  font-size: var(--app-text-sm);
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

.connect-alert {
  margin-bottom: 8px;
}

.snippet-tabs {
  margin-top: 4px;

  :deep(.t-tabs__content) {
    padding-top: 8px;
  }
}

/* Step rail, aligned with the embed / IM channel drawers. */
.im-steps {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  padding-bottom: 12px;
}

.im-step {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  padding: 0;
  border: none;
  background: transparent;
  font-family: inherit;
  text-align: left;
  cursor: pointer;

  &:hover:not(:disabled) {
    color: var(--td-text-color-secondary);
  }

  &:disabled {
    cursor: default;
  }
}

.im-step-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.im-step.active {
  color: var(--td-brand-color);
  font-weight: 500;
}

.im-step.done {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.im-step-num {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-xs);
  font-weight: 600;
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  background: transparent;
}

.im-step.active .im-step-num {
  background: var(--td-brand-color);
  color: #fff;
  border-color: var(--td-brand-color);
}

.im-step.done .im-step-num {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
  border-color: var(--td-component-stroke);
}

.im-step-check {
  font-size: var(--app-text-sm);
}

.im-step-body {
  display: flex;
  flex-direction: column;
}
</style>
