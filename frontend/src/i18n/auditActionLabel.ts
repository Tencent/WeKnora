import type { Composer } from 'vue-i18n'

import type { FlatKeyBagPath } from './auditActionRegistry.ts'

/**
 * Resolve a dot-namespaced audit action to its localized label.
 *
 * Uses `tm(root)` + flat key lookup because action strings such as
 * `rbac.member_added` must not be passed to `t()` — vue-i18n would split on
 * every dot and expect a nested object tree.
 */
export function auditActionLabel(
  i18n: Pick<Composer, 'tm'>,
  root: FlatKeyBagPath,
  action: string,
): string {
  const bag = i18n.tm(root) as unknown
  if (bag !== null && typeof bag === 'object' && typeof (bag as Record<string, string>)[action] === 'string') {
    return (bag as Record<string, string>)[action]
  }
  return action
}
