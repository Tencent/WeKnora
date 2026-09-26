import type { ContributionListing, ListedContribution, PluginContribution } from '@/api/plugin'
import { localizedText, type LocalizedText } from '../../utils/localizedText'

import { safeIconData } from '../pluginContributions'

/** The points whose contributions are sandboxed plugin pages. */
export const PAGE_POINTS = ['pages', 'settingsSections', 'kbTabs'] as const
export type PagePoint = (typeof PAGE_POINTS)[number]

export type PageRole = NonNullable<PluginContribution['minRole']>

/** One plugin page the workspace can show. */
export interface PluginPage {
  /** Unique across mounts: "plugin:<pluginId>/<id>". */
  key: string
  pluginId: string
  version: string
  id: string
  qualifiedId: string
  point: PagePoint
  /** What the page's requests name: "<point>/<id>". */
  mount: string
  name: LocalizedText
  description?: LocalizedText
  /** Icon file under ui/, if the plugin ships one. */
  icon?: string
  entry: string
  minRole: PageRole
  order: number
}

/** What a frame needs of a page: tool result pages are not listed pages. */
export type FramePage = Pick<PluginPage, 'pluginId' | 'version' | 'mount' | 'entry' | 'name'>

/** The role a page needs when the plugin names none. */
export function defaultMinRole(point: PagePoint): PageRole {
  return point === 'settingsSections' ? 'admin' : 'viewer'
}

function toPage(point: PagePoint, c: ListedContribution): PluginPage | null {
  if (!c.enabled || !c.entry || !c.version) return null
  return {
    key: `plugin:${c.qualifiedId}`,
    pluginId: c.pluginId,
    version: c.version,
    id: c.id,
    qualifiedId: c.qualifiedId,
    point,
    mount: `${point}/${c.id}`,
    name: c.name,
    description: c.description,
    icon: c.icon?.startsWith('ui/') ? c.icon : undefined,
    entry: c.entry,
    minRole: c.minRole ?? defaultMinRole(point),
    order: c.order ?? 0,
  }
}

/** The enabled pages of one point, in display order. */
export function pagesOf(listing: ContributionListing | null | undefined, point: PagePoint): PluginPage[] {
  const out: PluginPage[] = []
  for (const c of listing?.contributions?.[point] ?? []) {
    const page = toPage(point, c)
    if (page) out.push(page)
  }
  return out.sort((a, b) => a.order - b.order || a.qualifiedId.localeCompare(b.qualifiedId))
}

/**
 * The version of a plugin whose page files are served: undefined while the
 * listing is not loaded, null when the workspace no longer lists the plugin.
 */
export function listedVersion(listing: ContributionListing | null | undefined, pluginId: string): string | null | undefined {
  if (!listing) return undefined
  for (const list of Object.values(listing.contributions ?? {})) {
    const version = list?.find((c) => c.pluginId === pluginId && c.version)?.version
    if (version) return version
  }
  return null
}

/**
 * A recorded page (a tool result in chat history) as it can be served now:
 * only the loaded version's files are, so a page recorded under an older
 * version moves to the current one. Null when the plugin is gone; the
 * recorded page as is while the listing is unknown.
 */
export function currentFramePage(page: FramePage, listing: ContributionListing | null | undefined): FramePage | null {
  const version = listedVersion(listing, page.pluginId)
  if (version === null) return null
  return version && version !== page.version ? { ...page, version } : page
}

/** The URL of a file of a plugin's pages (the entry by default). */
export function pageFileUrl(apiBase: string, page: Pick<PluginPage, 'pluginId' | 'version' | 'entry'>, file = page.entry) {
  const seg = (s: string) => encodeURIComponent(s)
  const path = file.split('/').map(seg).join('/')
  return `${apiBase}/api/v1/plugin-ui/assets/${seg(page.pluginId)}/${seg(page.version)}/${path}`
}

/** Whether two listings of a page describe it alike (version, entry, name…). */
export function samePage(a: PluginPage, b: PluginPage): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** The page a key names, among pages. */
export function findPage(pages: readonly PluginPage[], key: string): PluginPage | undefined {
  return pages.find((p) => p.key === key)
}

/** A page's icon: its own file under ui/, else its plugin's icon. */
export function pageIconUrl(
  apiBase: string,
  listing: ContributionListing | null | undefined,
  page: Pick<PluginPage, 'pluginId' | 'version' | 'entry' | 'icon'>,
): string | undefined {
  if (page.icon) return pageFileUrl(apiBase, page, page.icon)
  return safeIconData(listing?.pluginIcons?.[page.pluginId])
}

/** The name of the plugin providing a page, or its ID. */
export function pageProvider(
  listing: ContributionListing | null | undefined,
  page: Pick<PluginPage, 'pluginId'>,
  locale: string,
): string {
  const name = listing?.pluginNames?.[page.pluginId]
  return (name && localizedText(name, locale)) || page.pluginId
}
