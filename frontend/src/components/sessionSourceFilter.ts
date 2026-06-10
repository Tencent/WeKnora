// Pure logic for the sidebar's session source filter (issue #1560).
//
// IM-channel sessions are auto-generated operational traffic, not the user's own
// chats, and IM is an optional module. So the sidebar defaults to web sessions
// and only surfaces a quiet source filter for tenants that actually configured a
// channel — keeping IM low-key rather than promoting it to a first-class group.

// 'web' = the user's own chats (no IM mapping); any other value is a platform
// key (e.g. 'feishu') selecting that channel's sessions. Sent verbatim as the
// `source` query param. The `(string & {})` arm keeps 'web' as an autocomplete
// hint instead of letting the union collapse to a bare `string`.
export type SessionSource = 'web' | (string & {})

export const DEFAULT_SESSION_SOURCE: SessionSource = 'web'

// The source filter control is only rendered once the tenant has at least one
// configured IM channel — tenants that never enabled IM see an unchanged,
// channel-free sidebar.
export function shouldShowSourceFilter(configured: string[]): boolean {
  return configured.length > 0
}

// Distinct platform keys from the tenant's IM channel overview, in first-seen
// order. Duplicates (multiple channels on one platform) collapse to one option,
// and malformed rows with no platform are dropped.
export function configuredPlatforms(channels: Array<{ platform: string }>): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const c of channels) {
    if (!c.platform || seen.has(c.platform)) continue
    seen.add(c.platform)
    out.push(c.platform)
  }
  return out
}

export interface SourceOption {
  value: SessionSource
  label: string
  // null for the user's own web chats; the platform key for an IM source.
  platform: string | null
}

// Build the source switcher's options: the user's own web chats first, then each
// configured IM platform in first-seen order. Labels are injected so this stays
// pure and unit-testable (no i18n dependency); logos are resolved by the caller
// (where the platform→asset map lives), keeping the dropdown component dumb.
export function buildSourceOptions(
  platforms: string[],
  webLabel: string,
  labelForPlatform: (platform: string) => string,
): SourceOption[] {
  return [
    { value: DEFAULT_SESSION_SOURCE, label: webLabel, platform: null },
    ...platforms.map((p) => ({ value: p, label: labelForPlatform(p), platform: p })),
  ]
}
