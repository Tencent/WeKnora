// Which plugin contribution an instance's type names, and whether the
// workspace switched that plugin off. Types are qualified IDs
// ("<plugin>/<local>") or, for builtins, their old short names (aliases).
import type { ContributionListing, ExtensionPoint, ListedContribution } from '../api/plugin'
import { localizedText } from '../utils/localizedText'

export function findContribution(
  listing: ContributionListing | null | undefined,
  point: ExtensionPoint,
  typeId: string | undefined,
): ListedContribution | undefined {
  if (!listing || !typeId) return undefined
  return (listing.contributions[point] ?? []).find(
    (c) => c.qualifiedId === typeId || (c.aliases ?? []).includes(typeId),
  )
}

/**
 * The contribution behind an instance when its plugin is switched off in
 * the workspace. Its instances keep working, but no new ones can be made.
 */
export function switchedOffContribution(
  listing: ContributionListing | null | undefined,
  point: ExtensionPoint,
  typeId: string | undefined,
): ListedContribution | undefined {
  const c = findContribution(listing, point, typeId)
  return c && !c.enabled ? c : undefined
}

/** The icon of a switched-off plugin behind an instance type, if it has one. */
export function switchedOffIcon(
  listing: ContributionListing | null | undefined,
  point: ExtensionPoint,
  typeId: string | undefined,
): string | undefined {
  const c = switchedOffContribution(listing, point, typeId)
  return c ? safeIconData(listing?.pluginIcons?.[c.pluginId]) : undefined
}

/** The chunking strategy value that names a plugin's chunker. */
export const PLUGIN_STRATEGY_PREFIX = 'plugin:'

/** A plugin chunker as a chunking strategy choice. */
export interface ChunkerStrategy {
  value: string
  label: string
  tooltip: string
  pluginId: string
  enabled: boolean
}

/**
 * The plugin chunkers a knowledge base can pick: those of plugins switched
 * on, plus the one it already uses (so a switched-off one still shows).
 */
export function chunkerStrategies(
  listing: ContributionListing | null | undefined,
  locale: string,
  current?: string,
): ChunkerStrategy[] {
  return (listing?.contributions.chunkers ?? [])
    .filter((c) => c.enabled || PLUGIN_STRATEGY_PREFIX + c.qualifiedId === current)
    .map((c) => ({
      value: PLUGIN_STRATEGY_PREFIX + c.qualifiedId,
      label: localizedText(c.name, locale) || c.qualifiedId,
      tooltip: localizedText(c.description, locale),
      pluginId: c.pluginId,
      enabled: c.enabled,
    }))
}

/** A plugin's icon data URI, when it is an image one (never a script). */
export function safeIconData(v: string | undefined): string | undefined {
  return v && /^data:image\/(svg\+xml|png|jpeg|webp);base64,[A-Za-z0-9+/=]+$/.test(v) ? v : undefined
}
