import type { ComposerTranslation } from 'vue-i18n'

import type { SyncItemError } from '@/api/datasource'

/**
 * Render one user-facing sync failure sample in the viewer's language.
 *
 * The backend sends a stable i18n `code` (+ `params`) so the reason can be
 * localised; `message` is the fallback for old logs / codes this client does
 * not know. The document title is kept separate and prefixed as
 * "title — reason", matching the sync-log drawer.
 */
export function formatSyncError(e: SyncItemError, t: ComposerTranslation): string {
  let reason = ''
  if (e.code) {
    const key = `datasource.syncError.${e.code}`
    const localised = t(key, (e.params || {}) as Record<string, unknown>)
    reason = localised === key ? (e.message || e.code) : localised
  } else {
    reason = e.message || ''
  }
  return e.title ? (reason ? `${e.title} — ${reason}` : e.title) : reason
}

/**
 * One-line error for a data-source card.
 *
 * The list response carries the same structured samples as the drawer in
 * `latest_sync_log.result.errors`, so prefer the localised first sample and
 * only fall back to the raw `error_message` (English, straight from the
 * backend) for old logs. Extra samples are summarised instead of piling up;
 * the full list stays in the sync-log drawer.
 */
export function formatSyncErrorSummary(
  errors: SyncItemError[] | undefined,
  fallback: string | undefined,
  t: ComposerTranslation,
): string {
  const samples = errors ?? []
  if (samples.length === 0) return fallback || ''
  const first = formatSyncError(samples[0], t)
  if (samples.length === 1) return first
  const more = t('datasource.syncErrorMore', { count: samples.length - 1 })
  return first ? `${first} ${more}` : more
}
