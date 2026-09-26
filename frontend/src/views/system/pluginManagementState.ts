// Pure helpers behind PluginManagement.vue, kept free of Vue so they run
// under node:test.
import type { ExtensionPoint, PluginManifest, PluginPermissions } from '../../api/plugin'
import type { InstalledPlugin, MarketPlugin, PluginVersion, TrustLevel, TrustVerdict } from '../../api/system/plugins'
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

/** The tag theme of a trust level: official and verified stand out. */
export function trustTheme(level: TrustLevel | undefined): 'success' | 'primary' | 'default' {
  if (level === 'official') return 'success'
  if (level === 'verified') return 'primary'
  return 'default'
}

/** What a package's signature says, as a pluginAdmin.trust message. */
export function trustNote(v: TrustVerdict): { key: 'signedBy' | 'untrustedKey' | 'unsigned'; keyId: string } {
  if (!v.keyId) return { key: 'unsigned', keyId: '' }
  return { key: v.trusted ? 'signedBy' : 'untrustedKey', keyId: v.keyId }
}

/** The workspaces a plugin is limited to, or null when every workspace sees it. */
export function audienceOf(p: Pick<InstalledPlugin, 'audience'> | null | undefined): number[] | null {
  return Array.isArray(p?.audience) ? [...p.audience].sort((a, b) => a - b) : null
}

/** Whether two audiences name the same workspaces (null: everyone). */
export function sameAudience(a: readonly number[] | null, b: readonly number[] | null): boolean {
  if (a === null || b === null) return a === b
  const x = [...new Set(a)].sort((m, n) => m - n)
  const y = [...new Set(b)].sort((m, n) => m - n)
  return x.length === y.length && x.every((v, i) => v === y[i])
}

/** Marketplace entries matching a search over name, ID, publisher and description. */
export function filterMarket(list: readonly MarketPlugin[], query: string, locale: string): MarketPlugin[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...list]
  return list.filter(p =>
    [
      p.id,
      localizedText(p.name, locale),
      localizedText(p.description, locale),
      p.publisher?.name ?? '',
      p.publisher?.id ?? '',
      ...(p.categories ?? []),
    ].some(s => s.toLowerCase().includes(q)),
  )
}

/** What picking a marketplace entry would do, for its button. */
export function marketAction(p: MarketPlugin): 'install' | 'upgrade' | 'installed' | 'unavailable' {
  if (!p.latest) return 'unavailable'
  if (!p.installedVersion) return 'install'
  return compareVersions(p.latest.version, p.installedVersion) > 0 ? 'upgrade' : 'installed'
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
export type InstalledState = 'running' | 'degraded' | 'failed' | 'disabled' | 'pending'

export function installedState(p: InstalledPlugin): InstalledState {
  if (p.desired_state === 'disabled') return 'disabled'
  if (!p.node) return 'pending'
  switch (p.node.state) {
    case 'ready':
      return 'running'
    case 'degraded':
      return 'degraded'
    default:
      return 'failed'
  }
}

/** The egress grant meaning any public host. */
export const EGRESS_ANY_HOST = '*'

/** Whether a string can be sent as a package URL. */
/**
 * Whether the install drawer can go ahead with a remote plugin's service URL:
 * a new install needs one, an upgrade may keep the registered one.
 */
export function remoteUrlReady(preview: { manifest: { runtime?: { type: string } }; change: string } | null, raw: string) {
  if (preview?.manifest.runtime?.type !== 'remote') return true
  if (raw.trim() === '') return preview.change !== 'install'
  return isPackageUrl(raw)
}

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

/** The filters above the installed-plugin table. */
export const ADMIN_FILTERS = ['all', 'problem', 'disabled', 'owned'] as const
export type AdminFilter = (typeof ADMIN_FILTERS)[number]

export function matchesAdminFilter(p: InstalledPlugin, f: AdminFilter): boolean {
  switch (f) {
    case 'problem': {
      const s = installedState(p)
      return s === 'failed' || s === 'degraded'
    }
    case 'disabled':
      return p.desired_state === 'disabled'
    case 'owned':
      return !!p.owner_tenant_id
    default:
      return true
  }
}

/** Installed plugins matching a filter and a search over ID and name, sorted by ID. */
export function filterInstalled(
  list: readonly InstalledPlugin[],
  f: { query: string; filter: AdminFilter; locale: string },
): InstalledPlugin[] {
  const q = f.query.trim().toLowerCase()
  return list
    .filter((p) => matchesAdminFilter(p, f.filter))
    .filter((p) => !q || [p.id, p.manifest ? localizedText(p.manifest.name, f.locale) : ''].some((s) => s.toLowerCase().includes(q)))
    .sort((a, b) => a.id.localeCompare(b.id))
}

/** The trust level of the version a plugin runs. */
export function activeTrust(p: InstalledPlugin): TrustLevel {
  return p.versions?.find((v) => v.version === p.active_version)?.trust ?? 'community'
}

/** Who can see a plugin, for the table's visibility column. */
export function audienceSummary(
  p: InstalledPlugin,
): { kind: 'owned'; tenant: number } | { kind: 'all' } | { kind: 'some'; count: number } {
  if (p.owner_tenant_id) return { kind: 'owned', tenant: p.owner_tenant_id }
  const a = audienceOf(p)
  return a === null ? { kind: 'all' } : { kind: 'some', count: a.length }
}
