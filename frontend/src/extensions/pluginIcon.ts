// The icon a plugin shows: the plugin's own (from its package, or a builtin
// vendor's logo, as a data: URI), else a builtin contribution's brand logo
// the app ships. undefined means "show the generic plugin icon".
import type { PluginManifest } from '@/api/plugin'
import { imPlatformLogo } from '@/components/imPlatformLogos'
import { getDatasourceIconUrl } from '@/views/knowledge/settings/datasourceIcons'
import { providerLogo, type LogoMatch } from '@/views/settings/providerLogos'

import { safeIconData } from './pluginContributions'

type IconSource = Pick<PluginManifest, 'iconData' | 'builtin' | 'contributes'> | undefined

/**
 * A plugin's logo: color logos render as images, mono ones as a mask in the
 * badge's own color.
 */
export function pluginLogo(m: IconSource): LogoMatch | undefined {
  const own = safeIconData(m?.iconData)
  if (own) return { mode: 'color', url: own }
  if (!m?.builtin) return undefined
  for (const c of m.contributes.connectors ?? []) {
    const url = getDatasourceIconUrl(c.id)
    if (url) return { mode: 'color', url }
  }
  for (const c of m.contributes.imChannels ?? []) {
    const url = imPlatformLogo(c.id)
    if (url) return { mode: 'color', url }
  }
  for (const [category, point] of [['websearch', 'webSearch'], ['parser', 'parsers']] as const) {
    for (const c of m.contributes[point] ?? []) {
      const logo = providerLogo(category, c.id)
      if (logo) return logo
    }
  }
  return undefined
}

/** A plugin's logo as an image URL, when it has a color one. */
export function pluginIconUrl(m: IconSource): string | undefined {
  const logo = pluginLogo(m)
  return logo?.mode === 'color' ? logo.url : undefined
}
