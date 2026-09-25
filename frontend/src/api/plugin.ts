import { get, put } from '@/utils/request'

import type { LocalizedText } from '@/utils/localizedText'

export type { LocalizedText } from '@/utils/localizedText'

export type ExtensionPoint = 'modelVendors' | 'connectors' | 'imChannels' | 'webSearch' | 'tools' | 'parsers'

export interface PluginContribution {
  id: string
  name: LocalizedText
  description?: LocalizedText
  icon?: string
  aliases?: string[]
  capabilities?: string[]
  order?: number
}

/** A plugin's manifest (internal/plugin/manifest.Manifest). */
export interface PluginManifest {
  schemaVersion: number
  id: string
  version: string
  name: LocalizedText
  description?: LocalizedText
  publisher: { id: string; name?: string; url?: string }
  icon?: string
  builtin?: boolean
  required?: boolean
  runtime: { type: string }
  contributes: Partial<Record<ExtensionPoint, PluginContribution[]>>
}

/** A plugin as the current workspace sees it. */
export interface TenantPlugin {
  manifest: PluginManifest
  enabled: boolean
  updatedAt?: string
}

export function listPlugins() {
  return get<{ data: TenantPlugin[] }>('/api/v1/plugins')
}

/** Turns a plugin on or off for the current workspace (Admin+). */
export function setPluginEnabled(id: string, enabled: boolean) {
  return put<{ data: TenantPlugin }>(`/api/v1/plugins/${encodeURIComponent(id)}/enabled`, { enabled })
}
