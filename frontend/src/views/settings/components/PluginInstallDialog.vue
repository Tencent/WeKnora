<template>
  <SettingDrawer v-model:visible="drawerVisible"
    :title="$t('settings.plugins.installDialog.title', { name: plugin?.name || '' })"
    :description="plugin?.description || $t('settings.plugins.installDialog.description')" icon="app" width="560px"
    :min-width="480" :max-width="760" storage-key="setting-drawer:width:plugin-install"
    :confirm-loading="installing" :confirm-disabled="!canInstall" :confirm-text="$t('settings.plugins.install')"
    @confirm="install">
    <div v-if="loading" class="install-dialog__loading"><t-loading size="small" /></div>

    <template v-else-if="preflight">
      <section class="setting-drawer__section">
        <p class="install-dialog__summary">
          <span v-if="preflight.skill_count > 0">{{ $t('settings.plugins.skillCount', { count: preflight.skill_count }) }}</span>
          <span v-if="preflight.mcp_count > 0">{{ $t('settings.plugins.mcpCount', { count: preflight.mcp_count }) }}</span>
        </p>
      </section>

      <section v-if="preflight.requires_sandbox" class="setting-drawer__section" data-testid="sandbox-section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.installDialog.sandboxTitle') }}</h4>
        <p v-if="hostOnly" class="install-dialog__hint">{{ $t('settings.plugins.installDialog.hostTarget') }}</p>
        <template v-else-if="sandboxes.length > 0">
          <p class="install-dialog__hint">{{ $t('settings.plugins.installDialog.sandboxHint') }}</p>
          <div class="install-dialog__sandboxes">
            <t-checkbox v-for="cfg in sandboxes" :key="cfg.id" :checked="selectedIds.includes(cfg.id)"
              :disabled="installing" @change="(checked: boolean) => toggleSandbox(cfg.id, checked)">
              <span class="install-dialog__sandbox">
                <SandboxBackendBadge :type="cfg.sandbox_type" size="sm" />
                <span>{{ cfg.name }}</span>
              </span>
            </t-checkbox>
          </div>
        </template>
        <div v-else class="install-dialog__empty" data-testid="no-sandbox">
          <p>{{ $t('settings.plugins.installDialog.noSandbox') }}</p>
          <t-button v-if="isAdmin" size="small" variant="outline" @click="goSandboxSettings">
            {{ $t('settings.plugins.installDialog.goSandboxSettings') }}
          </t-button>
        </div>
      </section>

      <section v-if="preflight.env_declarations?.length" class="setting-drawer__section" data-testid="env-section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.plugins.installDialog.envTitle') }}</h4>
        <p class="install-dialog__hint">{{ $t('settings.plugins.installDialog.envHint') }}</p>
        <div v-for="decl in preflight.env_declarations" :key="decl.name" class="install-dialog__env">
          <label :for="`plugin-env-${decl.name}`" class="install-dialog__env-label">
            <code>{{ decl.name }}</code>
            <span v-if="decl.required" class="install-dialog__required">{{ $t('settings.plugins.installDialog.envRequired') }}</span>
          </label>
          <t-input :id="`plugin-env-${decl.name}`" v-model="envValues[decl.name]" type="password"
            autocomplete="new-password" :disabled="installing"
            :placeholder="decl.description || $t('settings.plugins.installDialog.envPlaceholder')" />
          <p v-if="decl.description" class="install-dialog__env-desc">{{ decl.description }}</p>
        </div>
      </section>

      <section v-if="isAdmin && preflight.requires_sandbox && targetIds.length > 0" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallerModel') }}</h4>
        <p class="install-dialog__hint">{{ $t('settings.sandbox.skillInstallerModelHint') }}</p>
        <ModelSelector model-type="KnowledgeQA" :selected-model-id="installerModelId"
          :disabled="savingInstallerModel || installing" @update:selected-model-id="onInstallerModelChange" />
      </section>

      <p v-if="blockReason" class="install-dialog__block" role="status">{{ blockReason }}</p>
    </template>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import ModelSelector from '@/components/ModelSelector.vue'
import {
  installPlugin,
  preflightPlugin,
  type MarketplacePlugin,
  type PluginInstallResult,
  type PluginPreflight,
} from '@/api/plugin'
import { isNamedSandboxBackend, listSandboxConfigs, type SandboxConfigRecord } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useUIStore } from '@/stores/ui'
import { useSkillInstallerModel } from '@/composables/useSkillInstallerModel'
import { HOST_SKILL_TARGET_ID, hostSkillsOnly } from '@/utils/skillTarget'
import { filledEnv, missingRequiredEnv } from '@/utils/pluginMarketplace'

const props = defineProps<{
  visible: boolean
  marketplaceId: string
  plugin: MarketplacePlugin | null
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; installed: [result: PluginInstallResult] }>()
const { t } = useI18n()
const authStore = useAuthStore()
const capabilities = useDeploymentCapabilitiesStore()
const uiStore = useUIStore()
const isAdmin = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))
const {
  installerModelId, savingInstallerModel, loadInstallerModel, persistInstallerModel, onInstallerModelChange,
} = useSkillInstallerModel()

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const hostOnly = computed(() => hostSkillsOnly(
  capabilities.isSupported('settings.sandbox.remote'),
  capabilities.isSupported('settings.sandbox.host'),
))

const loading = ref(false)
const installing = ref(false)
const preflight = ref<PluginPreflight | null>(null)
const sandboxes = ref<SandboxConfigRecord[]>([])
const selectedIds = ref<string[]>([])
const envValues = reactive<Record<string, string>>({})

// Lite installs skills on this computer only; there is nothing to pick.
const targetIds = computed(() => {
  if (!preflight.value?.requires_sandbox) return []
  return hostOnly.value ? [HOST_SKILL_TARGET_ID] : selectedIds.value
})

const missingEnv = computed(() => missingRequiredEnv(preflight.value?.env_declarations, envValues))

const blockReason = computed(() => {
  if (!preflight.value) return ''
  if (preflight.value.requires_sandbox && targetIds.value.length === 0) {
    return t('settings.plugins.installDialog.sandboxRequired')
  }
  if (missingEnv.value.length > 0) {
    return t('settings.plugins.installDialog.missingEnv', { names: missingEnv.value.join(', ') })
  }
  return ''
})

const canInstall = computed(() => {
  if (!preflight.value || loading.value || installing.value || blockReason.value) return false
  if (!preflight.value.requires_sandbox) return true
  // Members use the installer model an admin already saved. They cannot
  // rewrite the built-in installer agent.
  return !isAdmin.value || Boolean(installerModelId.value)
})

watch(() => [props.visible, props.marketplaceId, props.plugin?.name] as const, ([open]) => {
  if (open) void load()
}, { immediate: true })

async function load() {
  const name = props.plugin?.name
  if (!props.marketplaceId || !name) return
  loading.value = true
  preflight.value = null
  selectedIds.value = []
  for (const key of Object.keys(envValues)) delete envValues[key]
  try {
    const res = await preflightPlugin(props.marketplaceId, name)
    preflight.value = res?.data || null
    for (const decl of preflight.value?.env_declarations || []) envValues[decl.name] = ''
    if (preflight.value?.requires_sandbox) {
      await loadInstallerModel()
      if (!hostOnly.value) {
        const configs = await listSandboxConfigs()
        sandboxes.value = (configs?.data || []).filter((cfg) => isNamedSandboxBackend(cfg.sandbox_type))
        if (sandboxes.value.length === 1) selectedIds.value = [sandboxes.value[0]!.id]
      }
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.loadFailed'))
  } finally {
    loading.value = false
  }
}

function toggleSandbox(id: string, checked: boolean) {
  selectedIds.value = checked
    ? [...new Set([...selectedIds.value, id])]
    : selectedIds.value.filter((existing) => existing !== id)
}

function goSandboxSettings() {
  drawerVisible.value = false
  uiStore.openSettings('sandbox')
}

async function install() {
  const name = props.plugin?.name
  if (!canInstall.value || !name) return
  installing.value = true
  try {
    if (isAdmin.value && preflight.value?.requires_sandbox) await persistInstallerModel(installerModelId.value)
    const res = await installPlugin(props.marketplaceId, name, {
      sandbox_config_ids: targetIds.value,
      env: filledEnv(envValues),
    })
    const result = res.data
    for (const warning of result.warnings || []) MessagePlugin.warning(warning)
    const needsOAuth = result.status === 'needs_oauth'
      || Object.values(result.mcp_oauth_states || {}).includes('needs_oauth')
    if (result.status === 'needs_oauth') {
      MessagePlugin.info(t('settings.plugins.toasts.needsOAuth'))
    } else {
      MessagePlugin.success(t(result.async_installation
        ? 'settings.plugins.toasts.installStarted'
        : 'settings.plugins.toasts.installed', { name }))
      if (needsOAuth) MessagePlugin.info(t('settings.plugins.toasts.needsOAuth'))
    }
    emit('installed', result)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.installFailed'))
  } finally {
    installing.value = false
  }
}
</script>

<style scoped lang="less">
.install-dialog__loading {
  padding: 40px 0;
  text-align: center;
}

.install-dialog__summary {
  display: flex;
  gap: 12px;
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.install-dialog__hint,
.install-dialog__env-desc {
  margin: 0 0 8px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.install-dialog__env-desc {
  margin: 4px 0 0;
}

.install-dialog__sandboxes {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.install-dialog__sandbox {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.install-dialog__empty {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-sm);

  p {
    margin: 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }
}

.install-dialog__env + .install-dialog__env {
  margin-top: 12px;
}

.install-dialog__env-label {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
  font-size: var(--app-text-sm);

  code {
    font-family: var(--app-font-mono, monospace);
  }
}

.install-dialog__required {
  color: var(--td-error-color);
  font-size: var(--app-text-xs);
}

.install-dialog__block {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-warning-color);
}
</style>
