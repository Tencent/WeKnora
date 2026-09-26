// Pure helpers behind PluginCenter.vue, kept free of Vue so they run under
// node:test.
import type { ExtensionPoint, PluginManifest, TenantPlugin } from '../../api/plugin'
import { localizedText } from '../../utils/localizedText'

/** Extension points in the order the plugin center shows them. */
export const EXTENSION_POINTS: ExtensionPoint[] = [
  'modelVendors', 'connectors', 'imChannels', 'webSearch', 'tools', 'parsers', 'skills', 'mcpServers',
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
