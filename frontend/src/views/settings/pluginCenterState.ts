// Pure helpers behind PluginCenter.vue, kept free of Vue so they run under
// node:test.
import type { ExtensionPoint, PluginManifest, TenantPlugin } from '../../api/plugin'
import { localizedText } from '../../utils/localizedText'

/** Extension points in the order the plugin center shows them. */
export const EXTENSION_POINTS: ExtensionPoint[] = [
  'modelVendors', 'connectors', 'imChannels', 'webSearch', 'tools', 'parsers',
]

/** How many contributions a plugin makes at each point, in point order. */
export function contributionSummary(m: PluginManifest): Array<{ point: ExtensionPoint; count: number }> {
  return EXTENSION_POINTS
    .map((point) => ({ point, count: m.contributes[point]?.length ?? 0 }))
    .filter((s) => s.count > 0)
}

export interface PluginFilter {
  query: string
  /** Only plugins contributing to this point; empty for all. */
  point: ExtensionPoint | ''
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
