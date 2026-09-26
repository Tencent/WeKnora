// Registers the builtin settings sections (settingsCatalog.ts) with their
// components, the same way a plugin registers its pages.
import type { Component } from 'vue'

import i18n from '@/i18n'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { hostSkillsOnly } from '@/utils/skillTarget'
import IntegrationSettingsSection from '@/views/integrations/IntegrationSettingsSection.vue'
import ChatHistorySettings from '@/views/settings/ChatHistorySettings.vue'
import EnvVarSettings from '@/views/settings/EnvVarSettings.vue'
import GeneralSettings from '@/views/settings/GeneralSettings.vue'
import MemorySettings from '@/views/settings/MemorySettings.vue'
import MemoryWorkspaceSettings from '@/views/settings/MemoryWorkspaceSettings.vue'
import ModelSettings from '@/views/settings/ModelSettings.vue'
import OllamaSettings from '@/views/settings/OllamaSettings.vue'
import ParserEngineSettings from '@/views/settings/ParserEngineSettings.vue'
import PluginCenter from '@/views/settings/PluginCenter.vue'
import SandboxSettings from '@/views/settings/SandboxSettings.vue'
import StorageBackendSettings from '@/views/settings/StorageBackendSettings.vue'
import SystemInfo from '@/views/settings/SystemInfo.vue'
import TenantInfo from '@/views/settings/TenantInfo.vue'
import TenantMembers from '@/views/settings/TenantMembers.vue'
import UserProfile from '@/views/settings/UserProfile.vue'
import VectorStoreSettings from '@/views/settings/VectorStoreSettings.vue'
import WebSearchSettings from '@/views/settings/WebSearchSettings.vue'
import WeKnoraCloudSettings from '@/views/settings/WeKnoraCloudSettings.vue'
import ModelCatalog from '@/views/system/ModelCatalog.vue'
import PlatformAPIKeys from '@/views/system/PlatformAPIKeys.vue'
import RuntimeQueues from '@/views/system/RuntimeQueues.vue'
import SystemAuditLog from '@/views/system/SystemAuditLog.vue'
import SystemSettings from '@/views/system/SystemSettings.vue'

import { CORE_PLUGIN_ID, settingsGroups, settingsSections } from '../settingsSections'
import { BUILTIN_SETTINGS_GROUPS, BUILTIN_SETTINGS_SECTIONS, type BuiltinSettingsSection } from './settingsCatalog'

const COMPONENTS: Record<string, Component> = {
  general: GeneralSettings,
  userprofile: UserProfile,
  mymemory: MemorySettings,
  envvars: EnvVarSettings,
  tenant: TenantInfo,
  members: TenantMembers,
  chathistory: ChatHistorySettings,
  memory: MemoryWorkspaceSettings,
  models: ModelSettings,
  ollama: OllamaSettings,
  weknoracloud: WeKnoraCloudSettings,
  vectorstore: VectorStoreSettings,
  parser: ParserEngineSettings,
  storage: StorageBackendSettings,
  sandbox: SandboxSettings,
  websearch: WebSearchSettings,
  plugins: PluginCenter,
  'system-global': SystemSettings,
  'model-catalog': ModelCatalog,
  'runtime-queues': RuntimeQueues,
  'platform-api-keys': PlatformAPIKeys,
  'system-audit-log': SystemAuditLog,
  system: SystemInfo,
}

const t = (key: string) => i18n.global.t(key)

/** Host-only skill deployments name the page after the host's secrets. */
function envVarsLabel(): string {
  const caps = useDeploymentCapabilitiesStore()
  const hostSkills = hostSkillsOnly(caps.isSupported('settings.sandbox.remote'), caps.isSupported('settings.sandbox.host'))
  const hostKey = 'envVarSettings.host.title'
  return hostSkills && i18n.global.te(hostKey) ? t(hostKey) : t('envVarSettings.title')
}

function labelOf(section: BuiltinSettingsSection): () => string {
  if (section.key === 'envvars') return envVarsLabel
  return section.literal ? () => section.label : () => t(section.label)
}

let registered = false

/** Registers the builtin settings groups and sections once. */
export function registerBuiltinSettings() {
  if (registered) return
  registered = true
  for (const group of BUILTIN_SETTINGS_GROUPS) {
    settingsGroups.register({ key: group.key, order: group.order, label: () => t(group.label) })
  }
  for (const section of BUILTIN_SETTINGS_SECTIONS) {
    const tab = section.integrationTab
    settingsSections.register({
      key: section.key,
      group: section.group,
      order: section.order,
      label: labelOf(section),
      icon: section.icon,
      component: tab ? IntegrationSettingsSection : COMPONENTS[section.key],
      props: tab ? () => ({ tab }) : undefined,
      layout: section.layout,
      pluginId: CORE_PLUGIN_ID,
      access: section.access,
    })
  }
}
