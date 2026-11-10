// cronHumanize maps known cron schedule presets to human-readable i18n keys.
// Falls back to the raw cron expression for unknown patterns.

const CRON_PRESET_MAP: Record<string, string> = {
  '0 */30 * * * *': 'datasource.scheduleHuman.30min',
  '0 0 * * * *': 'datasource.scheduleHuman.1h',
  '0 0 */6 * * *': 'datasource.scheduleHuman.6h',
  '0 0 */12 * * *': 'datasource.scheduleHuman.12h',
  '0 0 2 * * *': 'datasource.scheduleHuman.24h',
}

/**
 * Convert a cron expression to a human-readable string.
 * Uses i18n translate function for known presets, raw expression otherwise.
 */
export function humanizeCron(cron: string, t: (key: string) => string): string {
  const key = CRON_PRESET_MAP[cron]
  if (key) return t(key)
  return cron || '--'
}

/**
 * Format a timestamp as relative time (e.g. "3小时前", "2天前").
 * Falls back to locale date string for timestamps older than 30 days.
 */
export function relativeTime(ts: string | null, t: (key: string, params?: Record<string, string | number>) => string): string {
  if (!ts) return t('datasource.neverSynced')
  const then = new Date(ts).getTime()
  if (isNaN(then)) return t('datasource.neverSynced')
  const now = Date.now()
  const diffMs = now - then

  // Future time or just happened (within 60s)
  if (diffMs < 60000) return t('datasource.justNow')

  const minutes = Math.floor(diffMs / 60000)
  if (minutes < 60) return t('datasource.minutesAgo', { n: minutes })

  const hours = Math.floor(diffMs / 3600000)
  if (hours < 24) return t('datasource.hoursAgo', { n: hours })

  const days = Math.floor(diffMs / 86400000)
  if (days < 30) return t('datasource.daysAgo', { n: days })

  return new Date(ts).toLocaleDateString()
}
