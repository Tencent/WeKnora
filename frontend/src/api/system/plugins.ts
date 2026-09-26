// System-admin plugin installation (/api/v1/system/admin/plugins).
import { del, get, post, postUpload, put } from '@/utils/request'

import type { ConfigValue } from '@/components/schema-form/schema'
import type { PluginConfig, PluginManifest } from '@/api/plugin'

const BASE = '/api/v1/system/admin/plugins'
const PACKAGE_TIMEOUT = 5 * 60 * 1000

/** What installing a package would do. */
export type InstallChange = 'install' | 'upgrade' | 'downgrade' | 'reinstall'

export interface PluginPreview {
  manifest: PluginManifest
  digest: string
  size: number
  change: InstallChange
  installedVersion?: string
}

export interface PluginVersion {
  plugin_id: string
  version: string
  digest: string
  manifest: PluginManifest
  size: number
  created_by: string
  created_at: string
}

/** This node's report on an installed plugin. */
export interface PluginNodeStatus {
  version: string
  state: 'ready' | 'failed'
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
}

/** A package to inspect or install: an uploaded file or a URL. */
export type PackageSource = { file: File } | { url: string }

function packageForm(file: File, digest?: string) {
  const form = new FormData()
  form.append('file', file)
  if (digest) form.append('digest', digest)
  return form
}

export function listInstalledPlugins() {
  return get<{ data: InstalledPlugin[] }>(BASE)
}

export function getInstalledPlugin(id: string) {
  return get<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}`)
}

/** Reads a package and reports what installing it would do, changing nothing. */
export function inspectPluginPackage(source: PackageSource) {
  if ('file' in source) {
    return postUpload(`${BASE}/inspect`, packageForm(source.file), undefined, { timeout: PACKAGE_TIMEOUT }) as Promise<{
      data: PluginPreview
    }>
  }
  return post<{ data: PluginPreview }>(`${BASE}/inspect`, { url: source.url }, { timeout: PACKAGE_TIMEOUT })
}

/** Installs the package reviewed with inspect; digest pins it to that package. */
export function installPluginPackage(source: PackageSource, digest: string) {
  if ('file' in source) {
    return postUpload(BASE, packageForm(source.file, digest), undefined, { timeout: PACKAGE_TIMEOUT }) as Promise<{
      data: InstalledPlugin
    }>
  }
  return post<{ data: InstalledPlugin }>(BASE, { url: source.url, digest }, { timeout: PACKAGE_TIMEOUT })
}

export function setInstalledPluginEnabled(id: string, enabled: boolean) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/enabled`, { enabled })
}

export function activatePluginVersion(id: string, version: string) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/active-version`, { version })
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
