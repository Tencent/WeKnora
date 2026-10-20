/**
 * Where the settings close button should send the router.
 *
 * Vue Router stores the previous in-app location on `history.state.back`
 * and leaves it null when this URL was opened directly. `router.back()`
 * then leaves the app, so those closes go to the knowledge-base list.
 * Sections that already always went home keep doing so.
 */
export type SettingsCloseNavigation =
  | { type: 'push'; to: '/platform/knowledge-bases' }
  | { type: 'back' }
  | { type: 'none' }

const KNOWLEDGE_BASES_HOME = '/platform/knowledge-bases' as const

const DIRECT_HOME_SECTIONS = new Set([
  'model-catalog',
  'system-global',
  'runtime-queues',
  'platform-api-keys',
  'system-audit-log',
])

/** True when `history.state.back` is a previous in-app location. */
function hasInAppHistoryBack(historyState: unknown): boolean {
  if (!historyState || typeof historyState !== 'object') return false
  const back = (historyState as { back?: unknown }).back
  return typeof back === 'string' && back.startsWith('/') && !back.startsWith('//')
}

export function resolveSettingsCloseNavigation(
  path: string,
  section: unknown,
  historyState: unknown,
): SettingsCloseNavigation {
  if (path !== '/platform/settings') return { type: 'none' }
  if (
    (typeof section === 'string' && DIRECT_HOME_SECTIONS.has(section))
    || !hasInAppHistoryBack(historyState)
  ) {
    return { type: 'push', to: KNOWLEDGE_BASES_HOME }
  }
  return { type: 'back' }
}
