import { del, get, post, postUpload, put } from '@/utils/request'

export type PluginStatus = 'installing' | 'ready' | 'failed' | 'needs_oauth'

/** `unknown` means the probe timed out or failed; installation still succeeded. */
export type MCPProbeState = 'ready' | 'needs_oauth' | 'unknown'

/** Set on an MCP service or skill catalog that a plugin install owns. */
export interface PluginOwnership {
  plugin_install_id: string
  plugin_name: string
  marketplace_id: string
  marketplace_name?: string
}

export interface PluginEnvDeclaration {
  name: string
  description?: string
  required?: boolean
}

export interface MarketplacePlugin {
  name: string
  description?: string
  version?: string
  keywords?: string[]
  skill_count: number
  mcp_count: number
  requires_sandbox: boolean
  installed: boolean
  /** Version the workspace installed; lags `version` until the plugin is reinstalled. */
  installed_version?: string
  status?: PluginStatus
  install_id?: string
  error?: string
}

export interface PluginMarketplace {
  id: string
  name: string
  description?: string
  version?: string
  owner_name?: string
  owner_email?: string
  created_at: string
  updated_at: string
  plugins: MarketplacePlugin[]
  /** Set on the add response when some catalog entries were left out. */
  import_warnings?: string[]
}

export interface PluginPreflight {
  plugin_name: string
  skill_count: number
  mcp_count: number
  requires_sandbox: boolean
  env_declarations: PluginEnvDeclaration[]
}

export interface PluginInstallResult {
  install_id: string
  status: PluginStatus
  mcp_servers?: string[]
  skills?: string[]
  /** MCP service name → probe outcome. */
  mcp_oauth_states?: Record<string, MCPProbeState>
  warnings?: string[]
  async_installation: boolean
}

export interface PluginInstall {
  id: string
  tenant_id: number
  marketplace_id: string
  plugin_name: string
  version?: string
  description?: string
  sandbox_config_ids: string[]
  status: PluginStatus
  error?: string
  created_at: string
  updated_at: string
}

export interface PluginSkillInstall {
  catalog_id: string
  skill_name: string
  skill_id: string
  sandbox_config_id: string
  status: string
  error?: string
  version?: string
  updated_at: string
}

export interface PluginEnvStatus {
  name: string
  description?: string
  required?: boolean
  is_set: boolean
}

/** A plugin MCP service without any secret or header value. */
export interface PluginMCPService {
  id: string
  name: string
  description?: string
  url?: string
  transport_type: string
  enabled: boolean
  auth_config: {
    auth_type?: string
  }
}

export interface PluginDetail extends PluginInstall {
  marketplace_name?: string
  /** Version the marketplace ships now; differs from `version` until reinstalled. */
  latest_version?: string
  mcp_services?: PluginMCPService[]
  skills?: PluginSkillInstall[]
  env_status?: PluginEnvStatus[]
}

const base = '/api/v1/plugin-marketplaces'
const pluginPath = (marketplaceId: string, name: string) =>
  `${base}/${encodeURIComponent(marketplaceId)}/plugins/${encodeURIComponent(name)}`

export function listPluginMarketplaces() {
  return get<{ data: PluginMarketplace[] }>(base)
}

/** Adds or refreshes a marketplace from a git URL, `owner/repo` or archive URL. */
export function addPluginMarketplaceFromSource(source: string) {
  return post<{ data: PluginMarketplace }>(base, { source }, { timeout: 2 * 60 * 1000 })
}

/** Adds or refreshes a marketplace from a .zip / .tar.gz archive. */
export function addPluginMarketplaceFromFile(file: File | Blob, filename?: string, onProgress?: (percent: number) => void) {
  const form = new FormData()
  if (filename) form.append('file', file, filename)
  else form.append('file', file)
  return postUpload(base, form, (e: any) => {
    if (e.total) onProgress?.(Math.round((e.loaded * 100) / e.total))
  }, { timeout: 5 * 60 * 1000 }) as Promise<{ data: PluginMarketplace }>
}

export function deletePluginMarketplace(id: string) {
  return del(`${base}/${encodeURIComponent(id)}`)
}

export function preflightPlugin(marketplaceId: string, name: string) {
  return get<{ data: PluginPreflight }>(pluginPath(marketplaceId, name))
}

export function installPlugin(
  marketplaceId: string,
  name: string,
  body: { sandbox_config_ids: string[]; env: Record<string, string> },
) {
  return post<{ data: PluginInstallResult }>(`${pluginPath(marketplaceId, name)}/install`, body, {
    timeout: 2 * 60 * 1000,
  })
}

export function probePlugin(marketplaceId: string, name: string) {
  return post<{ data: Record<string, MCPProbeState> }>(`${pluginPath(marketplaceId, name)}/probe`, {})
}

export function updatePluginSandboxes(marketplaceId: string, name: string, sandboxConfigIds: string[]) {
  return put<{ data: boolean }>(`${pluginPath(marketplaceId, name)}/sandboxes`, {
    sandbox_config_ids: sandboxConfigIds,
  })
}

export function uninstallPlugin(marketplaceId: string, name: string) {
  return del<{ data: boolean }>(pluginPath(marketplaceId, name))
}

export function getPluginDetail(marketplaceId: string, name: string) {
  return get<{ data: PluginDetail }>(`${pluginPath(marketplaceId, name)}/detail`)
}
