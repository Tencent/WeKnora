// Pure helpers behind PluginCenter.vue, kept free of Vue so they run under
// node:test.
import type { ExtensionPoint, PluginManifest, TenantPlugin } from '../../api/plugin'
import { localizedText } from '../../utils/localizedText'

/** Extension points in the order the plugin center shows them. */
export const EXTENSION_POINTS: ExtensionPoint[] = [
  'modelVendors', 'connectors', 'imChannels', 'webSearch', 'tools', 'parsers', 'chunkers', 'pipelineHooks', 'skills', 'mcpServers',
  'pages', 'settingsSections', 'kbTabs', 'webhooks',
]

/** Whether the workspace can configure the plugin (it declares config.tenant). */
export function hasTenantConfig(m: PluginManifest): boolean {
  return !!m.config?.tenantSchema?.properties && Object.keys(m.config.tenantSchema.properties).length > 0
}

/** Whether the plugin has webhooks, whose URLs admins set up elsewhere. */
export function hasWebhooks(m: PluginManifest): boolean {
  return (m.contributes?.webhooks?.length ?? 0) > 0
}

/** Whether the plugin's configuration drawer has anything to show. */
export function canConfigure(m: PluginManifest): boolean {
  return hasTenantConfig(m) || hasWebhooks(m)
}

/** A webhook's full URL: the server's, or this page's origin with the path. */
export function webhookUrl(hook: { path: string; url?: string }, origin: string): string {
  return hook.url || `${origin}${hook.path}`
}

/**
 * Skills of enabled plugins, for the skill catalog's "from plugin" picker.
 * Each carries the install source the backend understands.
 */
export function pluginSkillChoices(
  list: readonly TenantPlugin[],
  locale: string,
): Array<{ source: string; name: string; plugin: string; description: string }> {
  const out: Array<{ source: string; name: string; plugin: string; description: string }> = []
  for (const p of list) {
    if (!p.enabled) continue
    for (const c of p.manifest.contributes.skills ?? []) {
      out.push({
        source: `plugin:${p.manifest.id}/${c.id}`,
        name: localizedText(c.name, locale) || c.id,
        plugin: localizedText(p.manifest.name, locale) || p.manifest.id,
        description: localizedText(c.description, locale),
      })
    }
  }
  return out.sort((a, b) => a.name.localeCompare(b.name, locale))
}

/** How many contributions a plugin makes at each point, in point order. */
export function contributionSummary(m: PluginManifest): Array<{ point: ExtensionPoint; count: number }> {
  return EXTENSION_POINTS
    .map((point) => ({ point, count: m.contributes[point]?.length ?? 0 }))
    .filter((s) => s.count > 0)
}

/**
 * The categories the plugin center filters by: what a plugin is for, in the
 * words of the people enabling it, rather than one chip per extension point.
 */
export const PLUGIN_CATEGORIES = {
  data: ['connectors', 'parsers', 'chunkers'],
  model: ['modelVendors'],
  tool: ['tools', 'mcpServers', 'skills', 'webSearch'],
  channel: ['imChannels', 'webhooks'],
  ui: ['pages', 'settingsSections', 'kbTabs', 'pipelineHooks'],
} as const satisfies Record<string, readonly ExtensionPoint[]>

export type PluginCategory = keyof typeof PLUGIN_CATEGORIES

export const CATEGORY_ORDER = Object.keys(PLUGIN_CATEGORIES) as PluginCategory[]

/** Whether a plugin contributes anything in a category. */
export function inCategory(m: PluginManifest, c: PluginCategory): boolean {
  return (PLUGIN_CATEGORIES[c] as readonly ExtensionPoint[]).some((p) => (m.contributes[p]?.length ?? 0) > 0)
}

/** The distinct kinds of thing a plugin provides, in point order (for a card's footer). */
export function providedPoints(m: PluginManifest): ExtensionPoint[] {
  const out: ExtensionPoint[] = []
  for (const s of contributionSummary(m)) {
    // Agent tools come as MCP servers or builtin tools; one label covers both.
    const p = s.point === 'mcpServers' ? 'tools' : s.point
    if (!out.includes(p)) out.push(p)
  }
  return out
}

/** The TDesign icon of each extension point, for capability lists. */
export const POINT_ICON: Record<ExtensionPoint, string> = {
  modelVendors: 'layers',
  connectors: 'data-base',
  imChannels: 'chat-message',
  webSearch: 'internet',
  tools: 'tools',
  parsers: 'file-1',
  chunkers: 'cut',
  pipelineHooks: 'ai-search',
  skills: 'star',
  mcpServers: 'tools',
  pages: 'app',
  settingsSections: 'setting-1',
  kbTabs: 'component-layout',
  webhooks: 'link',
}

/** A builtin's one category, for grouping: the first it belongs to. */
export function primaryCategory(m: PluginManifest): PluginCategory | undefined {
  return CATEGORY_ORDER.find((c) => inCategory(m, c))
}

/** One thing a plugin adds, with where to find it, for the detail drawer. */
export interface CapabilityLine {
  point: ExtensionPoint
  id: string
  name: string
  /** The MCP server URL, or the package path of a skill / vendor file. */
  detail: string
}

export function capabilityLines(m: PluginManifest, locale: string): CapabilityLine[] {
  const out: CapabilityLine[] = []
  for (const point of EXTENSION_POINTS) {
    for (const c of m.contributes[point] ?? []) {
      out.push({ point, id: `${point}/${c.id}`, name: localizedText(c.name, locale) || c.id, detail: c.mcp?.url ?? c.path ?? '' })
    }
  }
  return out
}

export interface PluginFilter {
  query: string
  /** Only plugins contributing to this point; empty for all. */
  point?: ExtensionPoint | ''
  /** Only plugins in this category; empty for all. */
  category?: PluginCategory | ''
  locale: string
}

/**
 * Plugins matching a search and point filter, sorted by display name. The
 * query matches the plugin name, ID and the names of its contributions, so
 * searching "lark" finds the Feishu plugin.
 */
export function filterPlugins(list: readonly TenantPlugin[], f: PluginFilter): TenantPlugin[] {
  const q = f.query.trim().toLowerCase()
  const matches = (p: TenantPlugin) => {
    const m = p.manifest
    if (f.point && !(m.contributes[f.point]?.length)) return false
    if (f.category && !inCategory(m, f.category)) return false
    if (!q) return true
    const texts = [m.id, localizedText(m.name, f.locale), m.name.default]
    for (const point of EXTENSION_POINTS) {
      for (const c of m.contributes[point] ?? []) {
        texts.push(c.id, localizedText(c.name, f.locale))
      }
    }
    return texts.some((t) => t.toLowerCase().includes(q))
  }
  return list
    .filter(matches)
    .sort((a, b) => localizedText(a.manifest.name, f.locale).localeCompare(localizedText(b.manifest.name, f.locale), f.locale))
}
