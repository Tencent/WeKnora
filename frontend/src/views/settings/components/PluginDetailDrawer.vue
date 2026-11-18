<template>
  <SettingDrawer v-model:visible="drawerVisible" :title="pluginName" :description="detail?.description || ''"
    icon="app" width="640px" :min-width="520" :max-width="920" storage-key="setting-drawer:width:plugin-detail"
    :confirm-text="$t('settings.plugins.detail.uninstall')"
    :confirm-disabled="!detail" :confirm-loading="uninstalling"
    :cancel-text="$t('common.close')" @confirm="askUninstall">
    <div v-if="loading && !detail" class="plugin-detail__loading"><t-loading size="small" /></div>

    <template v-else-if="detail">
      <section class="setting-drawer__section plugin-detail__meta">
        <span class="plugin-detail__status" :class="`is-${detail.status}`">
          <t-loading v-if="pluginStateIsTransient(detail.status)" size="12px" />
          {{ $t(`settings.plugins.status.${detail.status}`) }}
        </span>
        <span v-if="detail.marketplace_name">{{ $t('settings.plugins.detail.marketplace') }}: {{ detail.marketplace_name }}</span>
        <span v-if="detail.version">{{ $t('settings.plugins.detail.version') }}: {{ detail.version }}</span>
      </section>
      <p v-if="hasNewerVersion" class="plugin-detail__notice" data-testid="version-notice">
        {{ $t('settings.plugins.detail.newerVersion', { latest: detail.latest_version, installed: detail.version }) }}
      </p>
      <p v-if="detail.error" class="plugin-detail__error" role="alert">{{ detail.error }}</p>

      <section v-if="detail.mcp_services?.length" class="setting-drawer__section" data-testid="mcp-section">
        <div class="plugin-detail__section-head">
          <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.detail.mcpTitle') }}</h4>
          <t-button size="small" variant="text" :loading="probing" @click="probe">
            {{ $t('settings.plugins.detail.redetect') }}
          </t-button>
        </div>
        <ul class="plugin-detail__list">
          <li v-for="svc in detail.mcp_services" :key="svc.id" class="plugin-detail__row">
            <div class="plugin-detail__row-main">
              <button v-if="isAdmin" type="button" class="plugin-detail__row-name plugin-detail__row-name--link"
                :disabled="openingMcpId === svc.id"
                :aria-label="`${svc.name} · ${$t('settings.plugins.detail.openDetail')}`" @click="openMcp(svc)">
                <span class="plugin-detail__row-label">{{ svc.name }}</span>
                <t-loading v-if="openingMcpId === svc.id" size="12px" />
                <t-icon v-else name="chevron-right" size="14px" />
              </button>
              <span v-else class="plugin-detail__row-name">{{ svc.name }}</span>
              <span class="plugin-detail__row-meta">{{ svc.url }}</span>
            </div>
            <template v-if="svc.auth_config?.auth_type === 'oauth'">
              <span class="plugin-detail__pill" :class="{ 'is-ok': oauthAuthorized[svc.id] }">
                {{ $t(oauthAuthorized[svc.id] ? 'settings.plugins.detail.authorized' : 'settings.plugins.detail.notAuthorized') }}
              </span>
              <t-button size="small" :variant="oauthAuthorized[svc.id] ? 'text' : 'base'"
                :theme="oauthAuthorized[svc.id] ? 'default' : 'primary'" :loading="authorizingId === svc.id"
                @click="authorize(svc.id)">
                {{ $t(oauthAuthorized[svc.id] ? 'settings.plugins.detail.reauthorize' : 'settings.plugins.detail.authorize') }}
              </t-button>
            </template>
            <template v-else>
              <span v-if="probeStates[svc.name] === 'needs_oauth'" class="plugin-detail__pill is-failed"
                :title="$t('settings.plugins.detail.needsOAuthHint')">
                {{ $t('settings.plugins.detail.needsOAuth') }}
              </span>
              <span class="plugin-detail__pill">{{ authLabel(svc.auth_config?.auth_type) }}</span>
            </template>
          </li>
        </ul>
      </section>

      <section v-if="skillRows.length" class="setting-drawer__section" data-testid="skills-section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.detail.skillsTitle') }}</h4>
        <ul class="plugin-detail__list">
          <li v-for="row in skillRows" :key="`${row.skill_id}-${row.sandbox_config_id}`" class="plugin-detail__row">
            <div class="plugin-detail__row-main">
              <button v-if="isAdmin && canOpenSkill(row)" type="button" class="plugin-detail__row-name plugin-detail__row-name--link"
                :aria-label="`${row.skill_name} · ${$t('settings.plugins.detail.openDetail')}`" @click="openSkill(row)">
                <span class="plugin-detail__row-label">{{ row.skill_name }}</span>
                <t-icon name="chevron-right" size="14px" />
              </button>
              <span v-else class="plugin-detail__row-name">{{ row.skill_name }}</span>
              <span class="plugin-detail__row-meta">{{ sandboxName(row.sandbox_config_id) }}</span>
            </div>
            <span class="plugin-detail__pill" :class="`is-${row.status}`" :title="row.error || undefined">
              {{ skillStatusLabel(row.status) }}
            </span>
            <button v-if="isAdmin && row.catalog_id" type="button" class="plugin-detail__icon-btn"
              :title="$t('settings.sandbox.skillFiles')" :aria-label="$t('settings.sandbox.skillFiles')"
              @click="openSkillFiles(row)">
              <t-icon name="folder" size="14px" />
            </button>
            <t-button v-if="isAdmin && row.skill_id && recordFor(row.sandbox_config_id)" size="small" variant="text"
              @click="openProgress(row)">
              {{ $t('settings.plugins.detail.viewProgress') }}
            </t-button>
          </li>
        </ul>
      </section>

      <section v-if="detail.env_status?.length" class="setting-drawer__section" data-testid="env-section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.detail.envTitle') }}</h4>
        <ul class="plugin-detail__list">
          <li v-for="env in detail.env_status" :key="env.name" class="plugin-detail__row">
            <div class="plugin-detail__row-main">
              <code class="plugin-detail__row-name">{{ env.name }}</code>
              <span v-if="env.description" class="plugin-detail__row-meta">{{ env.description }}</span>
            </div>
            <span class="plugin-detail__pill" :class="{ 'is-ok': env.is_set, 'is-failed': !env.is_set && env.required }">
              {{ $t(env.is_set ? 'settings.plugins.detail.envSet' : 'settings.plugins.detail.envUnset') }}
            </span>
          </li>
        </ul>
      </section>

      <section v-if="hasSkills && !hostOnly" class="setting-drawer__section" data-testid="sandbox-section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.detail.sandboxesTitle') }}</h4>
        <p class="plugin-detail__hint">{{ $t('settings.plugins.detail.sandboxesHint') }}</p>
        <div class="plugin-detail__sandboxes">
          <t-checkbox v-for="cfg in sandboxes" :key="cfg.id" :checked="sandboxDraft.includes(cfg.id)"
            :disabled="savingSandboxes"
            @change="(checked: boolean) => toggleSandbox(cfg.id, checked)">
            <span class="plugin-detail__sandbox">
              <SandboxBackendBadge :type="cfg.sandbox_type" size="sm" />{{ cfg.name }}
            </span>
          </t-checkbox>
        </div>
        <t-button size="small" theme="primary" variant="outline" :loading="savingSandboxes"
          :disabled="!sandboxesChanged || sandboxDraft.length === 0" @click="saveSandboxes">
          {{ $t('settings.plugins.detail.saveSandboxes') }}
        </t-button>
      </section>
    </template>
  </SettingDrawer>

  <SettingDrawer v-model:visible="progressVisible" :title="progressTitle" icon="app" width="680px" :min-width="560"
    :max-width="920" storage-key="setting-drawer:width:plugin-skill-progress" :hide-footer="true" :z-index="2600">
    <SandboxSkillsPanel v-if="progressVisible && progressRecord && progressSkillId" :record="progressRecord"
      mode="list" hide-add :focus-skill-id="progressSkillId" @skills-changed="load(true)" />
  </SettingDrawer>

  <SkillFilesDrawer v-model:visible="filesVisible" :catalog-id="filesCatalogId" :skill-name="filesCatalogName" />

  <McpServiceDialog v-model:visible="mcpDialogVisible" :service="mcpDialogService" mode="edit" :z-index="2700"
    @success="onMcpSaved" />
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SandboxSkillsPanel from '@/components/SandboxSkillsPanel.vue'
import SkillFilesDrawer from '@/components/SkillFilesDrawer.vue'
import McpServiceDialog from './McpServiceDialog.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import {
  getPluginDetail,
  probePlugin,
  uninstallPlugin,
  updatePluginSandboxes,
  type MCPProbeState,
  type PluginDetail,
  type PluginMCPService,
  type PluginSkillInstall,
} from '@/api/plugin'
import {
  MCP_OAUTH_CALLBACK_PATH,
  getMCPOAuthAuthorizationStatus,
  getMCPOAuthAuthorizeURL,
  getMCPOAuthStatus,
  getMCPService,
  refreshMCPMetadata,
  type MCPService,
} from '@/api/mcp-service'
import { isNamedSandboxBackend, listSandboxConfigs, type SandboxConfigRecord } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { HOST_SKILL_TARGET_ID, hostSkillTargetRecord, hostSkillsOnly } from '@/utils/skillTarget'
import { pluginStateIsTransient } from '@/utils/pluginMarketplace'
import { openMcpOAuthPopup } from '@/utils/mcpOAuthPopup'

const props = defineProps<{ visible: boolean; marketplaceId: string; pluginName: string }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; changed: [] }>()
const { t, te } = useI18n()
const authStore = useAuthStore()
const capabilities = useDeploymentCapabilitiesStore()
const confirmDelete = useConfirmDelete()
const isAdmin = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})
const hostOnly = computed(() => hostSkillsOnly(
  capabilities.isSupported('settings.sandbox.remote'),
  capabilities.isSupported('settings.sandbox.host'),
))
const hostRecord = computed(() => hostSkillTargetRecord(t('settings.skills.hostTarget')))

const loading = ref(false)
const detail = ref<PluginDetail | null>(null)
const sandboxes = ref<SandboxConfigRecord[]>([])
const sandboxDraft = ref<string[]>([])
const savingSandboxes = ref(false)
const uninstalling = ref(false)
const probing = ref(false)
const authorizingId = ref('')
const oauthAuthorized = reactive<Record<string, boolean>>({})
const probeStates = ref<Record<string, MCPProbeState>>({})
const progressVisible = ref(false)
const progressRecord = ref<SandboxConfigRecord | null>(null)
const progressSkillId = ref('')
const progressTitle = ref('')
const filesVisible = ref(false)
const filesCatalogId = ref('')
const filesCatalogName = ref('')
const mcpDialogVisible = ref(false)
const mcpDialogService = ref<MCPService | null>(null)
const openingMcpId = ref('')
let pollTimer: number | null = null
let stopOAuthPopup: (() => void) | null = null
// Each load bumps the generation; a response for an older one (another plugin,
// or a reload that was overtaken) is dropped.
let loadGeneration = 0
let unmounted = false

const hasNewerVersion = computed(() => {
  const d = detail.value
  return !!d?.latest_version && !!d.version && d.latest_version !== d.version
})

const skillRows = computed<PluginSkillInstall[]>(() => detail.value?.skills || [])
const hasSkills = computed(() => skillRows.value.length > 0 || (detail.value?.sandbox_config_ids?.length ?? 0) > 0)
const sandboxesChanged = computed(() => {
  const current = new Set(detail.value?.sandbox_config_ids || [])
  return sandboxDraft.value.length !== current.size || sandboxDraft.value.some((id) => !current.has(id))
})

watch(() => [props.visible, props.marketplaceId, props.pluginName] as const, ([open]) => {
  stopPolling()
  probeStates.value = {}
  progressVisible.value = false
  filesVisible.value = false
  mcpDialogVisible.value = false
  if (!open) return
  detail.value = null
  void load()
  if (!hostOnly.value) {
    void listSandboxConfigs().then((res) => {
      sandboxes.value = (res?.data || []).filter((cfg) => isNamedSandboxBackend(cfg.sandbox_type))
    }).catch(() => {})
  }
}, { immediate: true })

async function load(silent = false) {
  if (!props.marketplaceId || !props.pluginName) return
  const generation = ++loadGeneration
  const stale = () => generation !== loadGeneration || unmounted
  if (!silent) loading.value = true
  try {
    const res = await getPluginDetail(props.marketplaceId, props.pluginName)
    if (stale()) return
    detail.value = res?.data || null
    sandboxDraft.value = [...(detail.value?.sandbox_config_ids || [])]
    void loadOAuthStates()
    schedulePoll()
  } catch (error: any) {
    if (stale()) return
    // The install row is gone once an uninstall finishes.
    if (error?.status === 404 && detail.value) {
      emit('changed')
      drawerVisible.value = false
      return
    }
    if (!silent) MessagePlugin.error(error?.message || t('settings.plugins.toasts.loadFailed'))
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}

function schedulePoll() {
  stopPolling()
  const d = detail.value
  if (!d || !props.visible || unmounted) return
  const skillBusy = (d.skills || []).some((s) => s.status === 'installing' || s.status === 'removing')
  const busy = pluginStateIsTransient(d.status) || skillBusy
  if (busy) pollTimer = window.setTimeout(() => { void load(true) }, 3000)
}

function stopPolling() {
  if (pollTimer) window.clearTimeout(pollTimer)
  pollTimer = null
}

async function loadOAuthStates() {
  for (const svc of detail.value?.mcp_services || []) {
    if (svc.auth_config?.auth_type !== 'oauth') continue
    try {
      oauthAuthorized[svc.id] = (await getMCPOAuthAuthorizationStatus(svc.id)).authorized
    } catch {
      oauthAuthorized[svc.id] = false
    }
  }
}

function authLabel(type: string | undefined) {
  if (type === 'bearer') return 'Bearer'
  if (type === 'api_key') return 'API Key'
  return t('settings.plugins.detail.noAuth')
}

function skillStatusLabel(status: string) {
  const key = `settings.plugins.status.${status}`
  return te(key) ? t(key) : status
}

function recordFor(id: string): SandboxConfigRecord | undefined {
  if (id === HOST_SKILL_TARGET_ID) return hostRecord.value
  return sandboxes.value.find((cfg) => cfg.id === id)
}

function sandboxName(id: string) {
  return recordFor(id)?.name || id
}

function openProgress(row: PluginSkillInstall) {
  const record = recordFor(row.sandbox_config_id)
  if (!record || !row.skill_id) return
  progressRecord.value = record
  progressSkillId.value = row.skill_id
  progressTitle.value = `${row.skill_name} · ${record.name}`
  progressVisible.value = true
}

function canOpenSkill(row: PluginSkillInstall) {
  return Boolean((row.skill_id && recordFor(row.sandbox_config_id)) || row.catalog_id)
}

function openSkillFiles(row: PluginSkillInstall) {
  if (!row.catalog_id) return
  filesCatalogId.value = row.catalog_id
  filesCatalogName.value = row.skill_name
  filesVisible.value = true
}

// The install detail is the same sandbox skill panel used on the skills page.
// A skill that is not on a sandbox yet opens its files instead.
function openSkill(row: PluginSkillInstall) {
  if (row.skill_id && recordFor(row.sandbox_config_id)) {
    openProgress(row)
    return
  }
  openSkillFiles(row)
}

async function openMcp(svc: PluginMCPService) {
  if (!svc.id || openingMcpId.value) return
  openingMcpId.value = svc.id
  try {
    mcpDialogService.value = await getMCPService(svc.id)
    mcpDialogVisible.value = true
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.loadFailed'))
  } finally {
    openingMcpId.value = ''
  }
}

function onMcpSaved() {
  mcpDialogVisible.value = false
  void load(true)
}

function toggleSandbox(id: string, checked: boolean) {
  sandboxDraft.value = checked
    ? [...new Set([...sandboxDraft.value, id])]
    : sandboxDraft.value.filter((existing) => existing !== id)
}

async function saveSandboxes() {
  savingSandboxes.value = true
  try {
    await updatePluginSandboxes(props.marketplaceId, props.pluginName, sandboxDraft.value)
    MessagePlugin.success(t('settings.plugins.toasts.sandboxesUpdated'))
    emit('changed')
    await load(true)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.sandboxesUpdateFailed'))
  } finally {
    savingSandboxes.value = false
  }
}

async function probe() {
  probing.value = true
  try {
    const res = await probePlugin(props.marketplaceId, props.pluginName)
    probeStates.value = res?.data || {}
    MessagePlugin.success(t('settings.plugins.toasts.probeDone'))
    await load(true)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.loadFailed'))
  } finally {
    probing.value = false
  }
}

function stopOAuthPoll() {
  stopOAuthPopup?.()
  stopOAuthPopup = null
  authorizingId.value = ''
}

async function authorize(serviceId: string) {
  stopOAuthPoll()
  authorizingId.value = serviceId
  try {
    const result = await openMcpOAuthPopup({
      win: window,
      serviceId,
      redirectUri: window.location.origin + MCP_OAUTH_CALLBACK_PATH,
      frontendRedirect: '/',
      authorize: getMCPOAuthAuthorizeURL,
      pollStatus: getMCPOAuthStatus,
      onAuthorized: async () => {
        stopOAuthPoll()
        oauthAuthorized[serviceId] = true
        MessagePlugin.success(t('settings.plugins.toasts.authorized'))
        emit('changed')
        await load(true)
        // Tools were not listable before consent; sync them now.
        try { await refreshMCPMetadata(serviceId) } catch { /* the MCP page can retry */ }
      },
      onClosed: () => { stopOAuthPoll() },
    })
    if (result.status === 'missing') throw new Error(t('settings.plugins.toasts.authorizeFailed'))
    if (result.status === 'opened') stopOAuthPopup = result.stop
  } catch (error: any) {
    stopOAuthPoll()
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.authorizeFailed'))
  }
}

function askUninstall() {
  if (!detail.value) return
  confirmDelete({
    body: t('settings.plugins.detail.uninstallBody', { name: props.pluginName }),
    onConfirm: async () => {
      uninstalling.value = true
      try {
        await uninstallPlugin(props.marketplaceId, props.pluginName)
        MessagePlugin.success(t('settings.plugins.toasts.uninstallStarted', { name: props.pluginName }))
        emit('changed')
        drawerVisible.value = false
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('settings.plugins.toasts.uninstallFailed'))
      } finally {
        uninstalling.value = false
      }
    },
  })
}

onUnmounted(() => {
  unmounted = true
  stopPolling()
  stopOAuthPoll()
})
</script>

<style scoped lang="less">
.plugin-detail__loading {
  padding: 40px 0;
  text-align: center;
}

.plugin-detail__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 16px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.plugin-detail__status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 500;

  &.is-ready { color: var(--td-success-color); }
  &.is-installing { color: var(--td-brand-color); }
  &.is-needs_oauth { color: var(--td-warning-color); }
  &.is-failed { color: var(--td-error-color); }
}

.plugin-detail__notice,
.plugin-detail__hint {
  margin: 0 0 8px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.plugin-detail__error {
  margin: 0 0 12px;
  padding: 8px 12px;
  border-radius: var(--app-radius-sm);
  background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  color: var(--td-error-color);
  font-size: var(--app-text-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.plugin-detail__section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.plugin-detail__list {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.plugin-detail__row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.plugin-detail__row-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.plugin-detail__row-name {
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.plugin-detail__row-name--link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: 100%;
  padding: 0;
  border: 0;
  background: none;
  cursor: pointer;
  text-align: left;

  &:hover:not(:disabled) {
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: default;
  }
}

.plugin-detail__row-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-detail__icon-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.plugin-detail__row-meta {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-detail__pill {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 18px;

  &.is-ok, &.is-ready {
    background: color-mix(in srgb, var(--td-success-color) 10%, transparent);
    color: var(--td-success-color);
  }

  &.is-installing, &.is-removing {
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
  }

  &.is-failed {
    background: color-mix(in srgb, var(--td-error-color) 10%, transparent);
    color: var(--td-error-color);
  }
}

.plugin-detail__sandboxes {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 12px;
}

.plugin-detail__sandbox {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
</style>
