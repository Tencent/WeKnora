// System-admin plugin installation (/api/v1/system/admin/plugins).
import { del, get, post, postUpload, put } from '@/utils/request'

import type { ConfigValue } from '@/components/schema-form/schema'
import type { PluginConfig, PluginInstance, PluginManifest } from '@/api/plugin'

const BASE = '/api/v1/system/admin/plugins'
const PACKAGE_TIMEOUT = 5 * 60 * 1000

/**
 * How far the platform trusts a package, from its signature: official and
 * verified packages are signed by a key in the platform's trust store.
 */
export type TrustLevel = 'official' | 'verified' | 'community'

export interface TrustVerdict {
  level: TrustLevel
  /** The key that signed the package, trusted or not. */
  keyId?: string
  /** The signing key is in the platform's trust store. */
  trusted?: boolean
}

/** What installing a package would do. */
export type InstallChange = 'install' | 'upgrade' | 'downgrade' | 'reinstall'

export interface PluginPreview {
  manifest: PluginManifest
  digest: string
  size: number
  change: InstallChange
  trust: TrustVerdict
  installedVersion?: string
}

export interface PluginVersion {
  plugin_id: string
  version: string
  digest: string
  manifest: PluginManifest
  size: number
  /** Trust when the version was stored; older rows read community. */
  trust?: TrustLevel
  signer_key_id?: string
  created_by: string
  created_at: string
}

/** This node's report on an installed plugin. */
export interface PluginNodeStatus {
  version: string
  // degraded: loaded, but its process crashed or fails health checks and is
  // being restarted.
  state: 'ready' | 'failed' | 'degraded'
  error?: string
  updatedAt: string
}

export interface InstalledPlugin {
  id: string
  source: { kind: 'upload' | 'url'; url?: string }
  active_version: string
  desired_state: 'enabled' | 'disabled'
  runtime: string
  granted_perms: PluginManifest['permissions']
  created_by: string
  created_at: string
  updated_at: string
  manifest?: PluginManifest
  versions: PluginVersion[]
  node?: PluginNodeStatus
  /** Set for a workspace's own plugin: the workspace that registered it. */
  owner_tenant_id?: number
  /** The workspaces the plugin is limited to; absent when every workspace sees it. */
  audience?: number[] | null
  /** Where a remote plugin's service runs. */
  remote_url?: string
  /** A remote plugin's new signing secret, only in the response that issued it. */
  issuedSecret?: string
}

/**
 * A package to inspect or install: an uploaded file or a URL. A digest given
 * with a URL (a marketplace listing's) must match what is downloaded.
 */
export type PackageSource = { file: File } | { url: string; digest?: string }

/** A published package in the marketplace index. */
export interface MarketVersion {
  version: string
  url: string
  digest: string
  size?: number
  engines?: { weknora?: string }
  runtime?: string
}

/** A marketplace entry as this platform sees it. */
export interface MarketPlugin {
  id: string
  name: PluginManifest['name']
  description?: PluginManifest['description']
  publisher: PluginManifest['publisher']
  icon?: string
  homepage?: string
  categories?: string[]
  versions: MarketVersion[]
  /** The newest version this WeKnora runs; null when none does. */
  latest: MarketVersion | null
  incompatible?: string
  installedVersion?: string
}

export interface MarketListing {
  configured: boolean
  indexUrl?: string
  plugins: MarketPlugin[]
  /** Index entries left out as malformed. */
  skipped?: string[]
}

/** The marketplace index WEKNORA_PLUGIN_INDEX_URL names. */
export function listMarketPlugins() {
  return get<{ data: MarketListing }>(`${BASE}/market`, { timeout: 30_000 })
}

function packageForm(file: File, digest?: string, remoteUrl?: string) {
  const form = new FormData()
  form.append('file', file)
  if (digest) form.append('digest', digest)
  if (remoteUrl) form.append('remote_url', remoteUrl)
  return form
}

export function listInstalledPlugins() {
  return get<{ data: InstalledPlugin[] }>(BASE)
}

export function getInstalledPlugin(id: string) {
  return get<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}`)
}

/** Where a plugin runs, on every node, whichever workspaces see it. */
export function getPluginInstances(id: string) {
  return get<{ data: { instances: PluginInstance[]; instanceError?: string } }>(`${BASE}/${encodeURIComponent(id)}/instances`)
}

/** Reviewing and installing packages, against one install endpoint. */
export interface PackageEndpoints {
  /** Reads a package and reports what installing it would do, changing nothing. */
  inspect(source: PackageSource): Promise<{ data: PluginPreview }>
  /**
   * Installs the package reviewed with inspect; digest pins it to that
   * package. A remote plugin also needs the URL of its service (optional on
   * upgrades).
   */
  install(source: PackageSource, digest: string, remoteUrl?: string): Promise<{ data: InstalledPlugin }>
}

export function packageEndpoints(base: string): PackageEndpoints {
  return {
    inspect(source) {
      if ('file' in source) {
        return postUpload(`${base}/inspect`, packageForm(source.file), undefined, {
          timeout: PACKAGE_TIMEOUT,
        }) as Promise<{ data: PluginPreview }>
      }
      return post<{ data: PluginPreview }>(
        `${base}/inspect`,
        { url: source.url, digest: source.digest },
        { timeout: PACKAGE_TIMEOUT },
      )
    },
    install(source, digest, remoteUrl) {
      if ('file' in source) {
        return postUpload(base, packageForm(source.file, digest, remoteUrl), undefined, {
          timeout: PACKAGE_TIMEOUT,
        }) as Promise<{ data: InstalledPlugin }>
      }
      return post<{ data: InstalledPlugin }>(
        base,
        { url: source.url, digest, remote_url: remoteUrl || undefined },
        { timeout: PACKAGE_TIMEOUT },
      )
    },
  }
}

/** The platform's plugin installation. */
export const platformPackages = packageEndpoints(BASE)
export const inspectPluginPackage = platformPackages.inspect
export const installPluginPackage = platformPackages.install

export function setInstalledPluginEnabled(id: string, enabled: boolean) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/enabled`, { enabled })
}

export function activatePluginVersion(id: string, version: string) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/active-version`, { version })
}

/** Workspaces for the audience picker: by keyword (name or ID), or by IDs. */
export function listAudienceTenants(q: { keyword?: string; ids?: number[] }) {
  const params = new URLSearchParams()
  if (q.keyword) params.set('keyword', q.keyword)
  if (q.ids?.length) params.set('ids', q.ids.join(','))
  const qs = params.toString()
  return get<{ data: Array<{ id: number; name: string }> }>(`${BASE}/tenants${qs ? `?${qs}` : ''}`)
}

/** Limits a plugin to some workspaces; null lets every workspace see it. */
export function setPluginAudience(id: string, tenants: number[] | null) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/audience`, { tenants })
}

export function setPluginRemoteUrl(id: string, url: string) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/remote-url`, { url })
}

/** Issues a new signing secret; the response carries it in issuedSecret. */
export function rotatePluginSecret(id: string) {
  return post<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/secret/rotate`, {})
}

export function uninstallPlugin(id: string) {
  return del(`${BASE}/${encodeURIComponent(id)}`)
}

export function getPluginSystemConfig(id: string) {
  return get<{ data: PluginConfig }>(`${BASE}/${encodeURIComponent(id)}/config`)
}

export function updatePluginSystemConfig(id: string, values: ConfigValue) {
  return put<{ data: PluginConfig }>(`${BASE}/${encodeURIComponent(id)}/config`, { values })
}
