// The icon a plugin card shows: the plugin's own (from its package, or a
// builtin vendor's logo, as a data: URI), else a builtin contribution's
// brand logo the app ships. undefined means "show the initial".
import type { PluginManifest } from '@/api/plugin'
import { getDatasourceIconUrl } from '@/views/knowledge/settings/datasourceIcons'
import { providerLogo } from '@/views/settings/providerLogos'

import { safeIconData } from './pluginContributions'

export function pluginIconUrl(
  m: Pick<PluginManifest, 'iconData' | 'builtin' | 'contributes'> | undefined,
): string | undefined {
  const own = safeIconData(m?.iconData)
  if (own || !m?.builtin) return own
  for (const c of m.contributes.connectors ?? []) {
    const url = getDatasourceIconUrl(c.id)
    if (url) return url
  }
  for (const [category, point] of [['websearch', 'webSearch'], ['parser', 'parsers']] as const) {
    for (const c of m.contributes[point] ?? []) {
      const logo = providerLogo(category, c.id)
      if (logo?.mode === 'color') return logo.url
    }
  }
  return undefined
}
