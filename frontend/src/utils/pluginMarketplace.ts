import JSZip from 'jszip'
import type { MarketplacePlugin, PluginEnvDeclaration, PluginStatus } from '@/api/plugin'

export const MARKETPLACE_MANIFEST_PATH = '.weknora-plugin/marketplace.json'

/** Catalog files, in the same order the backend picks when several are present. */
export const MARKETPLACE_MANIFEST_PATHS = [
  MARKETPLACE_MANIFEST_PATH,
  '.claude-plugin/marketplace.json',
  '.cursor-plugin/marketplace.json',
  '.github/plugin/marketplace.json',
  '.agents/plugins/marketplace.json',
]

const PLUGIN_MARKERS = [
  'plugin.json',
  '.weknora-plugin/plugin.json',
  '.claude-plugin/plugin.json',
  '.cursor-plugin/plugin.json',
  '.codex-plugin/plugin.json',
  'gemini-extension.json',
  'mcp.json',
  '.mcp.json',
  'SKILL.md',
]

const ARCHIVE_SUFFIXES = ['.zip', '.tar.gz', '.tgz', '.tar']

/**
 * Filter for the archive file input.
 *
 * Chromium's file dialog matches the suffix after the last dot, and on Linux
 * it also matches the file's MIME type. A `.tar.gz` file is therefore a `.gz`
 * / `application/gzip` / `application/x-compressed-tar` file, and listing only
 * `.tar.gz` leaves it grayed out. A plain `.gz` can be picked, then
 * `isMarketplaceArchiveName` rejects it.
 */
export const MARKETPLACE_ARCHIVE_ACCEPT = [
  '.zip',
  '.tar.gz',
  '.tgz',
  '.tar',
  '.gz',
  'application/zip',
  'application/gzip',
  'application/x-gzip',
  'application/x-tar',
  'application/x-compressed-tar',
].join(',')

export function isMarketplaceArchiveName(name: string): boolean {
  const lower = name.trim().toLowerCase()
  return ARCHIVE_SUFFIXES.some((suffix) => lower.endsWith(suffix))
}

/**
 * Accepts the forms the backend resolves: `owner/repo`, an http(s) git host
 * URL, or a direct archive URL. Anything else is rejected before the request.
 */
export function isMarketplaceSource(source: string): boolean {
  const value = source.trim()
  if (!value) return false
  if (/^[A-Za-z0-9][\w.-]*\/[\w.-]+$/.test(value)) return true
  try {
    const url = new URL(value)
    return url.protocol === 'https:' || url.protocol === 'http:'
  } catch {
    return false
  }
}

export interface FolderFile {
  /** Path relative to the picked folder, as `webkitRelativePath` reports it. */
  path: string
  data: Blob | ArrayBuffer | Uint8Array | string
}

/**
 * Path of each file inside the archive. A browser folder pick prefixes every
 * path with the folder name; it is kept, and the backend strips one wrapping
 * directory around the manifest.
 */
function archivePath(path: string): string {
  return path.replace(/\\/g, '/').replace(/^\/+/, '')
}

function layoutIsImportable(paths: string[]): boolean {
  const set = new Set(paths)
  if (paths.some((path) => MARKETPLACE_MANIFEST_PATHS.includes(path))) return true
  if (PLUGIN_MARKERS.some((marker) => set.has(marker))) return true
  if (paths.some((path) => path.startsWith('skills/') && path.endsWith('/SKILL.md'))) return true
  const children = new Set<string>()
  for (const path of paths) {
    const slash = path.indexOf('/')
    if (slash > 0) children.add(path.slice(0, slash))
  }
  for (const child of children) {
    if (child.startsWith('.')) continue
    if (PLUGIN_MARKERS.some((marker) => set.has(`${child}/${marker}`))) return true
    if (set.has(`${child}/SKILL.md`)) return true
    const skills = `${child}/skills/`
    if (paths.some((path) => path.startsWith(skills) && path.endsWith('/SKILL.md'))) return true
  }
  return false
}

/**
 * True when the picked folder is a marketplace catalog or a plugin directory.
 * A browser folder pick prefixes every path with the folder name, so the
 * manifest may sit one level down. Deeper vendor copies do not count.
 */
export function folderHasMarketplaceManifest(rawPaths: string[]): boolean {
  const paths = rawPaths.map(archivePath).filter(Boolean)
  if (layoutIsImportable(paths)) return true
  if (paths.length > 0 && paths.every((path) => path.includes('/'))) {
    return layoutIsImportable(paths.map((path) => path.slice(path.indexOf('/') + 1)))
  }
  return false
}

/** Zips a picked folder so it uploads the same way as a .zip archive. */
export async function zipMarketplaceFolder(files: FolderFile[]): Promise<Blob> {
  const zip = new JSZip()
  for (const file of files) {
    const path = archivePath(file.path)
    if (!path || path.split('/').some((part) => part === '..')) continue
    const data = typeof file.data === 'object' && 'arrayBuffer' in file.data
      ? await file.data.arrayBuffer()
      : file.data
    zip.file(path, data)
  }
  return zip.generateAsync({ type: 'blob', compression: 'DEFLATE' })
}

/** Names of required variables the user has not filled in. */
export function missingRequiredEnv(
  declarations: PluginEnvDeclaration[] | undefined,
  values: Record<string, string>,
): string[] {
  return (declarations || [])
    .filter((decl) => decl.required && !(values[decl.name] || '').trim())
    .map((decl) => decl.name)
}

/** Drops blank values so an optional variable left empty is not sent. */
export function filledEnv(values: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, value] of Object.entries(values)) {
    if (value.trim()) out[name] = value
  }
  return out
}

export type PluginCardState = 'available' | PluginStatus

export function pluginCardState(plugin: MarketplacePlugin): PluginCardState {
  if (!plugin.installed) return 'available'
  return plugin.status || 'ready'
}

/** An install is still settling; keep polling until it leaves this state. */
export function pluginStateIsTransient(state: string | undefined): boolean {
  return state === 'installing'
}

/** Installed plugins across marketplaces. The toolbox badge uses the same count. */
export function installedPluginCount(marketplaces: Array<{ plugins?: Array<{ installed?: boolean }> }>): number {
  return marketplaces.reduce((total, mp) => total + (mp.plugins?.filter((plugin) => plugin.installed).length ?? 0), 0)
}
/**
 * Whether a marketplace still has installed plugins. It reads the full list,
 * not a search-filtered copy that may hide the installed ones.
 */
export function marketplaceHasInstalls(
  marketplaces: Array<{ id: string; plugins?: Array<{ installed?: boolean }> }>,
  id: string,
): boolean {
  return !!marketplaces.find((mp) => mp.id === id)?.plugins?.some((plugin) => plugin.installed)
}

/** True when the marketplace ships a different version than the one installed. */
export function pluginHasNewerVersion(plugin: Pick<MarketplacePlugin, 'installed' | 'version' | 'installed_version'>): boolean {
  return !!plugin.installed && !!plugin.version && !!plugin.installed_version && plugin.version !== plugin.installed_version
}
