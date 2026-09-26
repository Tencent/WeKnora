// The builtin settings sections as data: which page sits in which sidebar
// group, in what order, with what icon and access rules. Components are
// attached in settings.ts, so this file stays importable under node:test.
import { INTEGRATION_PREVIEW_ITEMS, INTEGRATION_TAB_CAPABILITY, INTEGRATION_TAB_MIN_ROLE } from '../../config/integrations'
import { SETTINGS_SECTION_CAPABILITY, skillSettingsSupported } from '../../config/deploymentCapabilities'
import { SETTINGS_SECTION_MIN_ROLE, SYSTEM_ADMIN_SETTINGS_SECTIONS } from '../../config/settingsAccess'
import { integrationSectionKey } from '../../config/settingsRoute'

import type { SettingsNavIcon, SettingsSection, SettingsSectionAccess } from '../settingsSections'

/** A sidebar group; `label` is a locale key. */
export interface BuiltinSettingsGroup {
  key: string
  order: number
  label: string
}

/** A builtin section; `label` is a locale key unless `literal` is set. */
export interface BuiltinSettingsSection {
  key: string
  group: string
  order: number
  label: string
  literal?: boolean
  icon: SettingsNavIcon
  layout?: SettingsSection['layout']
  /** Integration tab rendered by IntegrationSettingsSection. */
  integrationTab?: string
  access: SettingsSectionAccess
}

/** Sidebar groups: account → workspace → models → integrations → data → plugins → system → platform. */
export const BUILTIN_SETTINGS_GROUPS: BuiltinSettingsGroup[] = [
  { key: 'account', order: 10, label: 'settings.navGroups.account' },
  { key: 'workspace', order: 20, label: 'settings.navGroups.workspace' },
  { key: 'models_runtime', order: 30, label: 'settings.navGroups.modelsRuntime' },
  { key: 'integrations', order: 40, label: 'integrations.title' },
  { key: 'data_extensions', order: 50, label: 'settings.navGroups.dataExtensions' },
  { key: 'plugins', order: 55, label: 'pluginCenter.navGroup' },
  { key: 'system_administration', order: 60, label: 'settings.navGroups.systemAdministration' },
  { key: 'platform', order: 70, label: 'settings.navGroups.platform' },
]

const icon = (name: string): SettingsNavIcon => ({ kind: 'tdesign', name })

/** Access for a workspace section, from the shared role and capability tables. */
function access(key: string): SettingsSectionAccess {
  if (SYSTEM_ADMIN_SETTINGS_SECTIONS.has(key)) {
    return { systemAdminOnly: true, capability: SETTINGS_SECTION_CAPABILITY[key] }
  }
  return { minRole: SETTINGS_SECTION_MIN_ROLE[key] ?? 'viewer', capability: SETTINGS_SECTION_CAPABILITY[key] }
}

type Row = Omit<BuiltinSettingsSection, 'group' | 'order' | 'access'> & { access?: SettingsSectionAccess }

const ROWS: Record<string, Row[]> = {
  account: [
    { key: 'general', label: 'general.title', icon: icon('setting') },
    { key: 'userprofile', label: 'userProfile.title', icon: icon('user') },
    { key: 'mymemory', label: 'memorySettings.title', icon: icon('bookmark') },
    // Label switches to envVarSettings.host.title on host-only skill setups.
    {
      key: 'envvars', label: 'envVarSettings.title', icon: icon('key'),
      access: { minRole: SETTINGS_SECTION_MIN_ROLE.envvars, supported: skillSettingsSupported },
    },
  ],
  workspace: [
    { key: 'tenant', label: 'settings.tenantInfo', icon: icon('user-circle') },
    { key: 'members', label: 'tenantMember.title', icon: icon('usergroup'), layout: 'wide' },
    { key: 'chathistory', label: 'chatHistorySettings.title', icon: icon('chat') },
    { key: 'memory', label: 'memoryWorkspaceSettings.title', icon: icon('bulletpoint') },
  ],
  models_runtime: [
    { key: 'models', label: 'settings.modelManagement', icon: icon('control-platform') },
    { key: 'ollama', label: 'Ollama', literal: true, icon: icon('server') },
    { key: 'weknoracloud', label: 'WeKnora Cloud', literal: true, icon: { kind: 'builtin', name: 'weknora' } },
  ],
  integrations: INTEGRATION_PREVIEW_ITEMS.map((item) => ({
    key: integrationSectionKey(item.key),
    label: `integrations.tabs.${item.key}`,
    icon: item.icon.type === 'icon' ? icon(item.icon.name) : { kind: 'emoji' as const, value: item.icon.value },
    layout: 'full' as const,
    integrationTab: item.key,
    access: { minRole: INTEGRATION_TAB_MIN_ROLE[item.key], capability: INTEGRATION_TAB_CAPABILITY[item.key] },
  })),
  data_extensions: [
    { key: 'vectorstore', label: 'settings.vectorStoreEngine', icon: icon('data-base') },
    { key: 'parser', label: 'settings.parserEngine', icon: icon('file-search') },
    { key: 'storage', label: 'settings.storageEngine', icon: icon('cloud') },
    { key: 'sandbox', label: 'settings.sandbox.title', icon: { kind: 'builtin', name: 'sandbox' } },
    { key: 'websearch', label: 'settings.webSearchConfig', icon: { kind: 'builtin', name: 'globe' } },
  ],
  // Everyone can see which plugins the workspace has; only admins switch them.
  plugins: [
    { key: 'plugins', label: 'pluginCenter.title', icon: icon('app') },
  ],
  system_administration: [
    { key: 'system-global', label: 'settings.system', icon: icon('server') },
    { key: 'model-catalog', label: 'modelCatalog.title', icon: icon('control-platform') },
    { key: 'runtime-queues', label: 'settings.taskQueue', icon: icon('queue') },
    { key: 'platform-api-keys', label: 'platformApiKeys.title', icon: icon('secured') },
    { key: 'system-audit-log', label: 'system.globalSettings.audit.tabLabel', icon: icon('history') },
    { key: 'plugin-admin', label: 'pluginAdmin.title', icon: icon('app') },
  ],
  platform: [
    { key: 'system', label: 'settings.versionInfo', icon: icon('info-circle') },
  ],
}

/** Every builtin section, in sidebar order within each group. */
export const BUILTIN_SETTINGS_SECTIONS: BuiltinSettingsSection[] = Object.entries(ROWS).flatMap(([group, rows]) =>
  rows.map((row, i) => ({
    ...row,
    group,
    order: (i + 1) * 10,
    access: row.access ?? access(row.key),
    // System-admin pages use the full content width.
    layout: SYSTEM_ADMIN_SETTINGS_SECTIONS.has(row.key) ? 'full' : row.layout,
  })),
)
