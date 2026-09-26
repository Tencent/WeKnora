import { get, post, put } from '@/utils/request'

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
  | 'chunkers'
  | 'pipelineHooks'
  | 'skills'
  | 'mcpServers'
  | 'pages'
  | 'settingsSections'
  | 'kbTabs'
  | 'webhooks'

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
  /** The HTML page of a UI contribution (pages, settingsSections, kbTabs), under ui/. */
  entry?: string
  /** Workspace role a UI contribution needs. */
  minRole?: 'viewer' | 'contributor' | 'admin' | 'owner'
}

/** A contribution as GET /plugins/contributions lists it. */
export interface ListedContribution extends PluginContribution {
  pluginId: string
  qualifiedId: string
  version?: string
  enabled: boolean
}

export interface ContributionListing {
  points: Array<{ point: ExtensionPoint; thirdParty: boolean; declarative: boolean }>
  contributions: Partial<Record<ExtensionPoint, ListedContribution[]>>
  /** Icons (data URIs) of the plugins the workspace switched off. */
  pluginIcons?: Record<string, string>
  /** Names of the listed plugins. */
  pluginNames?: Record<string, LocalizedText>
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
  /** The icon as a data: URI (from the package, or a builtin vendor's logo). */
  iconData?: string
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

/** Every contribution the deployment has, with the workspace's switches. */
export function listContributions(point?: ExtensionPoint) {
  return get<{ data: ContributionListing }>('/api/v1/plugins/contributions', point ? { params: { point } } : undefined)
}

/** What a plugin page's request returned. */
export interface PluginPageResponse {
  status: number
  body?: unknown
}

/** Relays a request from a plugin page to the plugin's backend. */
export function pluginPageRequest(
  pluginId: string,
  req: { mount: string; method: string; path: string; body?: unknown },
) {
  return post<{ data: PluginPageResponse }>(`/api/v1/plugins/${encodeURIComponent(pluginId)}/ui-request`, req)
}

/** A webhook of a plugin with this workspace's URL (Admin+: the URL is a secret). */
export interface PluginWebhook {
  id: string
  name: LocalizedText
  description?: LocalizedText
  /** Path on this deployment; `url` is the full URL when the server knows its public address. */
  path: string
  url?: string
}

export function listPluginWebhooks(id: string) {
  return get<{ data: PluginWebhook[] }>(`/api/v1/plugins/${encodeURIComponent(id)}/webhooks`)
}

/** The workspace's configuration of a plugin (Admin+). */
export function getPluginConfig(id: string) {
  return get<{ data: PluginConfig }>(`/api/v1/plugins/${encodeURIComponent(id)}/config`)
}

/** Saves the workspace's configuration; "***" keeps a stored secret (Admin+). */
export function updatePluginConfig(id: string, values: ConfigValue) {
  return put<{ data: PluginConfig }>(`/api/v1/plugins/${encodeURIComponent(id)}/config`, { values })
}

/** Which configuration a plugin form edits. */
export type PluginFormScope = 'system' | 'tenant' | 'instance'

export interface PluginOptionsRequest {
  name: string
  field: string
  scope: PluginFormScope
  /** "<point>/<id>" of the instance form ("connectors/jira"). */
  contribution?: string
  /** The instance being edited; its stored secrets fill in redacted ones. */
  instanceId?: string
  /** What the form holds now, in the scope's shape. */
  values: ConfigValue
  query?: string
}

/** A field's choices from the plugin (x-options). */
export function pluginFormOptions(pluginId: string, req: PluginOptionsRequest) {
  return post<{ data: { options: Array<{ value: string; label?: string; description?: string }> } }>(
    `/api/v1/plugins/${encodeURIComponent(pluginId)}/options`, req,
  )
}

/** Starts connecting an x-oauth field; the tokens stay on the server. */
export function startPluginOAuth(
  pluginId: string,
  req: { scope: PluginFormScope; contribution?: string; field: string },
) {
  return post<{ data: { authorizeUrl: string; state: string; redirectUri: string } }>(
    `/api/v1/plugins/${encodeURIComponent(pluginId)}/oauth/start`, req,
  )
}

/** How an authorization ended, collected once; pending until it comes back. */
export function pluginOAuthResult(pluginId: string, state: string) {
  return get<{ data: { status: 'pending' | 'done'; ok?: boolean; connection?: string; error?: string } }>(
    `/api/v1/plugins/${encodeURIComponent(pluginId)}/oauth/result?state=${encodeURIComponent(state)}`,
  )
}
