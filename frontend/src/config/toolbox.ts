import { SETTINGS_SECTION_MIN_ROLE, type SettingsRoleKey } from './settingsAccess'
import { SETTINGS_SECTION_CAPABILITY, type DeploymentCapabilityKey } from './deploymentCapabilities'
import { SKILL_ICON } from '../types/mention'

export const TOOLBOX_ITEMS = [
  {
    key: 'skills',
    icon: SKILL_ICON,
    title: 'settings.skills.title',
    description: 'settings.skills.description',
    help: 'settings.skills.helpTooltip',
    action: 'settings.skills.addSkill',
  },
  {
    key: 'mcp',
    icon: 'tools',
    title: 'settings.mcpService',
    description: 'mcpSettings.description',
    action: 'mcpSettings.addService',
  },
  {
    key: 'browserconnection',
    title: 'localBrowser.settingsTitle',
    description: 'localBrowser.settingsDescription',
  },
] as const

export type ToolboxSection = typeof TOOLBOX_ITEMS[number]['key']

export function isToolboxSection(section: unknown): section is ToolboxSection {
  return TOOLBOX_ITEMS.some((item) => item.key === section)
}

export function toolboxLocation(section?: ToolboxSection, sandboxId?: string) {
  return {
    path: section ? `/platform/toolbox/${section}` : '/platform/toolbox',
    query: section === 'skills' && sandboxId ? { sandboxId } : {},
  }
}

/** Use the same role and deployment gates as the original settings panels. */
export function canAccessToolboxSection(
  section: ToolboxSection,
  access: {
    currentTenantRole: string | null | undefined
    canAccessAllTenants: boolean
    hasRole: (role: SettingsRoleKey) => boolean
    isSupported: (capability?: DeploymentCapabilityKey) => boolean
  },
): boolean {
  if (!access.currentTenantRole && !access.canAccessAllTenants) return false
  return (access.canAccessAllTenants || access.hasRole(SETTINGS_SECTION_MIN_ROLE[section] ?? 'viewer'))
    && access.isSupported(SETTINGS_SECTION_CAPABILITY[section])
}
