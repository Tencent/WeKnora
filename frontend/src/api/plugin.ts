import { get, put } from '@/utils/request'

import type { ConfigSchema, ConfigValue } from '@/components/schema-form/schema'
import type { LocalizedText } from '@/utils/localizedText'

export type { LocalizedText } from '@/utils/localizedText'

export type ExtensionPoint =
  | 'modelVendors'
  | 'connectors'
  | 'imChannels'
  | 'webSearch'
  | 'tools'
  | 'parsers'
  | 'skills'
  | 'mcpServers'

export interface PluginContribution {
  id: string
  name: LocalizedText
  description?: LocalizedText
  icon?: string
  aliases?: string[]
  capabilities?: string[]
  order?: number
  /** Package path of a skill directory or model vendor file. */
  path?: string
  /** Remote MCP server an installed plugin contributes. */
  mcp?: { url: string; transport?: string; headers?: Record<string, string> }
}

export interface PluginPermissions {
  egress?: string[]
  hostApi?: string[]
  events?: string[]
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
  homepage?: string
  license?: string
  builtin?: boolean
  required?: boolean
  engines?: { weknora?: string }
  runtime: { type: string }
  permissions?: PluginPermissions
  config?: {
    system?: string
    tenant?: string
    systemSchema?: ConfigSchema
    tenantSchema?: ConfigSchema
  }
  contributes: Partial<Record<ExtensionPoint, PluginContribution[]>>
}

/** A plugin as the current workspace sees it. */
export interface TenantPlugin {
  manifest: PluginManifest
  enabled: boolean
  updatedAt?: string
}

/** One running instance of a plugin, for the detail view. */
export interface PluginInstance {
  node: string
  version: string
  state: 'starting' | 'ready' | 'degraded' | 'stopped'
  error?: string
  updatedAt: string
}

/** A plugin configuration: its schema and values, secrets redacted. */
export interface PluginConfig {
  schema: ConfigSchema
  values: ConfigValue
  updatedAt?: string
}

export function listPlugins() {
  return get<{ data: TenantPlugin[] }>('/api/v1/plugins')
}

export function getPlugin(id: string) {
  return get<{ data: TenantPlugin & { instances: PluginInstance[]; instanceError?: string } }>(
    `/api/v1/plugins/${encodeURIComponent(id)}`,
  )
}

/** Turns a plugin on or off for the current workspace (Admin+). */
export function setPluginEnabled(id: string, enabled: boolean) {
  return put<{ data: TenantPlugin }>(`/api/v1/plugins/${encodeURIComponent(id)}/enabled`, { enabled })
}

/** The workspace's configuration of a plugin (Admin+). */
export function getPluginConfig(id: string) {
  return get<{ data: PluginConfig }>(`/api/v1/plugins/${encodeURIComponent(id)}/config`)
}

/** Saves the workspace's configuration; "***" keeps a stored secret (Admin+). */
export function updatePluginConfig(id: string, values: ConfigValue) {
  return put<{ data: PluginConfig }>(`/api/v1/plugins/${encodeURIComponent(id)}/config`, { values })
}
