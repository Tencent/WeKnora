import type { Component } from 'vue'

import type { DeploymentCapabilityKey, DeploymentCapabilityMap } from '@/config/deploymentCapabilities'
import type { SettingsRoleKey } from '@/config/settingsAccess'

import { createRegistry } from './registry'

/** Plugin ID of the settings every WeKnora build ships. */
export const CORE_PLUGIN_ID = 'weknora.core'

/** How a section is drawn in the settings sidebar. */
export type SettingsNavIcon =
  | { kind: 'tdesign'; name: string }
  | { kind: 'emoji'; value: string }
  /** Hand-drawn icons kept in SettingsNavIcon.vue. */
  | { kind: 'builtin'; name: 'globe' | 'weknora' | 'sandbox' }

export interface SettingsGroup {
  key: string
  order: number
  label: () => string
}

/** Who may see a section and whether this deployment supports it. */
export interface SettingsSectionAccess {
  minRole?: SettingsRoleKey
  systemAdminOnly?: boolean
  capability?: DeploymentCapabilityKey
  /** Overrides `capability` when support depends on several capabilities. */
  supported?: (capabilities: DeploymentCapabilityMap) => boolean
}

/** One page of the settings modal. */
export interface SettingsSection {
  /** Also the `?section=` value in the URL. */
  key: string
  group: string
  /** Sort order within the group (lower first). */
  order: number
  label: () => string
  icon: SettingsNavIcon
  component: Component
  props?: () => Record<string, unknown>
  /** Sub-entries under the section in the sidebar. */
  children?: Array<{ key: string; label: string }>
  /** Content width: wide for tables, full for pages with their own layout. */
  layout?: 'wide' | 'full'
  pluginId: string
  access: SettingsSectionAccess
}

export const settingsGroups = createRegistry<SettingsGroup>('settings group')
export const settingsSections = createRegistry<SettingsSection>('settings section')

/** What the current user and deployment allow, for sectionAllowed. */
export interface SettingsAccessContext {
  isSystemAdmin: boolean
  canAccessAllTenants: boolean
  hasRole: (role: SettingsRoleKey) => boolean
  isSupported: (capability?: DeploymentCapabilityKey) => boolean
  capabilities: DeploymentCapabilityMap
}

/** Whether the user may open a section at all (role and system-admin gates). */
export function sectionPermitted(section: SettingsSection, ctx: SettingsAccessContext): boolean {
  const { access } = section
  if (access.systemAdminOnly) return ctx.isSystemAdmin
  if (ctx.canAccessAllTenants) return true
  return ctx.hasRole(access.minRole ?? 'viewer')
}

/** Whether this deployment supports a section. */
export function sectionSupported(section: SettingsSection, ctx: SettingsAccessContext): boolean {
  const { access } = section
  if (access.supported) return access.supported(ctx.capabilities)
  return ctx.isSupported(access.capability)
}

export interface SettingsNavGroup {
  key: string
  label: string
  items: SettingsSection[]
}

/**
 * Sidebar groups in group order, each with its visible sections in section
 * order. Groups without a visible section are dropped, and so are sections
 * whose group is not registered.
 */
export function groupSections(
  groups: readonly SettingsGroup[],
  visible: readonly SettingsSection[],
): SettingsNavGroup[] {
  return [...groups]
    .sort((a, b) => a.order - b.order)
    .map((group) => ({
      key: group.key,
      label: group.label(),
      items: visible.filter((s) => s.group === group.key).sort((a, b) => a.order - b.order),
    }))
    .filter((group) => group.items.length > 0)
}
