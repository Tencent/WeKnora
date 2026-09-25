// Pure helpers behind PluginManagement.vue, kept free of Vue so they run
// under node:test.
import type { ExtensionPoint, PluginManifest, PluginPermissions } from '../../api/plugin'
import type { InstalledPlugin, PluginVersion } from '../../api/system/plugins'
import { localizedText } from '../../utils/localizedText'
import { EXTENSION_POINTS } from '../settings/pluginCenterState'

/** Human-readable size: 1.2 MB. */
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '-'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

/** The short form of a sha256 digest shown next to versions. */
export function shortDigest(digest: string): string {
  const hex = digest.replace(/^sha256:/, '')
  return hex.slice(0, 12)
}

/** One line of what a package would add, for the install review. */
export interface ContributionLine {
  point: ExtensionPoint
  id: string
  name: string
  /** The MCP server URL, or the package path of a skill / vendor file. */
  detail: string
}

export function contributionLines(m: PluginManifest, locale: string): ContributionLine[] {
  const out: ContributionLine[] = []
  for (const point of EXTENSION_POINTS) {
    for (const c of m.contributes[point] ?? []) {
      out.push({
        point,
        id: `${m.id}/${c.id}`,
        name: localizedText(c.name, locale) || c.id,
        detail: c.mcp?.url ?? c.path ?? '',
      })
    }
  }
  return out
}

/** Permission kinds a manifest can request, in review order. */
export type PermissionKind = keyof PluginPermissions

/** The permissions a package asks for, flattened for review. */
export function permissionLines(p: PluginPermissions | undefined): Array<{ kind: PermissionKind; value: string }> {
  const out: Array<{ kind: PermissionKind; value: string }> = []
  for (const kind of ['egress', 'hostApi', 'events'] as PermissionKind[]) {
    for (const value of p?.[kind] ?? []) out.push({ kind, value })
  }
  return out
}

/** Hosts a package's MCP servers talk to, which the review calls out. */
export function remoteHosts(m: PluginManifest): string[] {
  const hosts = new Set<string>()
  for (const c of m.contributes.mcpServers ?? []) {
    if (!c.mcp?.url) continue
    try {
      hosts.add(new URL(c.mcp.url).host)
    } catch {
      hosts.add(c.mcp.url)
    }
  }
  return [...hosts].sort()
}

function semverParts(v: string): number[] {
  return v.split(/[-+]/)[0].split('.').map((x) => Number.parseInt(x, 10) || 0)
}

/** Compares two semantic versions by major.minor.patch. */
export function compareVersions(a: string, b: string): number {
  const pa = semverParts(a)
  const pb = semverParts(b)
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) - (pb[i] ?? 0)
  }
  return 0
}

/** Stored versions, newest first. */
export function sortVersions(list: readonly PluginVersion[]): PluginVersion[] {
  return [...list].sort((a, b) => compareVersions(b.version, a.version))
}

/** Overall state of an installed plugin on the node that answered. */
export type InstalledState = 'running' | 'failed' | 'disabled' | 'pending'

export function installedState(p: InstalledPlugin): InstalledState {
  if (p.desired_state === 'disabled') return 'disabled'
  if (!p.node) return 'pending'
  return p.node.state === 'ready' ? 'running' : 'failed'
}

/** Whether a string can be sent as a package URL. */
export function isPackageUrl(raw: string): boolean {
  try {
    const u = new URL(raw.trim())
    return (u.protocol === 'https:' || u.protocol === 'http:') && !!u.host
  } catch {
    return false
  }
}

/** Whether the plugin declares a platform-wide configuration. */
export function hasSystemConfig(m: PluginManifest | undefined): boolean {
  const props = m?.config?.systemSchema?.properties
  return !!props && Object.keys(props).length > 0
}
