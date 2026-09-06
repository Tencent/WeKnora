export const EMPTY_ANALYTICS_VALUE = '—'

export interface CallCompositionItem {
  type: string
  label: string
  count: number
}

// Runtime keys may precede a corresponding TypeScript contract update.
export function normalizeCallComposition(
  calls: object,
  knownLabels: Readonly<Record<string, string>>,
  otherLabel: string,
): CallCompositionItem[] | null {
  const total = (calls as { total?: unknown }).total
  if (typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0) return null
  const items: CallCompositionItem[] = []
  for (const [type, count] of Object.entries(calls)) {
    if (type === 'total' || typeof count !== 'number' || !Number.isFinite(count) || count < 0) continue
    if (count === 0) continue
    const readable = type.replace(/[_-]+/g, ' ').trim()
    const fallback = readable ? readable.charAt(0).toUpperCase() + readable.slice(1) : otherLabel
    items.push({
      type,
      label: Object.hasOwn(knownLabels, type) ? knownLabels[type] : fallback,
      count,
    })
  }
  const sum = items.reduce((value, item) => value + item.count, 0)
  if (!Number.isFinite(sum) || sum > total) return null
  if (sum < total) {
    // Keep a residual key distinct even if the API supplies its own "other" field.
    let type = '__residual_other'
    while (items.some(item => item.type === type)) type += '_'
    items.push({ type, label: otherLabel, count: total - sum })
  }
  return items
}

// Geometry uses unrounded counts and the original response total.
export function buildCallComposition(items: readonly CallCompositionItem[], total: number) {
  if (
    !Number.isSafeInteger(total) || total < 0
    || items.some(item => !Number.isFinite(item.count) || item.count < 0)
    || items.reduce((sum, item) => sum + item.count, 0) !== total
  ) return null

  let cumulative = 0
  return items.filter(item => item.count > 0).map(item => {
    const offset = cumulative / total
    cumulative += item.count
    return { ...item, ratio: item.count / total, offset }
  })
}

export type AnalyticsDateRange = [string, string]

function padDatePart(value: number): string {
  return String(value).padStart(2, '0')
}

export function formatLocalCalendarDate(date: Date): string {
  return `${date.getFullYear()}-${padDatePart(date.getMonth() + 1)}-${padDatePart(date.getDate())}`
}

function parseLocalCalendarDate(value: string): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) throw new Error(`invalid calendar date: ${value}`)
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const parsed = new Date(year, month - 1, day)
  if (
    parsed.getFullYear() !== year
    || parsed.getMonth() !== month - 1
    || parsed.getDate() !== day
  ) {
    throw new Error(`invalid calendar date: ${value}`)
  }
  return parsed
}

export function defaultAnalyticsDateRange(now = new Date()): AnalyticsDateRange {
  const end = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const start = new Date(end)
  start.setDate(start.getDate() - 29)
  return [formatLocalCalendarDate(start), formatLocalCalendarDate(end)]
}

export function inclusiveDateRangeToExclusiveRFC3339(range: readonly string[]): {
  startTime: string
  endTime: string
} {
  if (range.length !== 2) throw new Error('analytics date range requires two dates')
  const start = parseLocalCalendarDate(range[0])
  const inclusiveEnd = parseLocalCalendarDate(range[1])
  if (start.getTime() > inclusiveEnd.getTime()) {
    throw new Error('analytics date range start must not be after end')
  }
  const exclusiveEnd = new Date(inclusiveEnd)
  exclusiveEnd.setDate(exclusiveEnd.getDate() + 1)
  return { startTime: start.toISOString(), endTime: exclusiveEnd.toISOString() }
}

export function formatExactNumber(value: number | null, locale = 'en-US'): string {
  if (value === null) return EMPTY_ANALYTICS_VALUE
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 20 }).format(value)
}

export function formatCompactNumber(value: number | null, locale = 'en-US'): string {
  if (value === null) return EMPTY_ANALYTICS_VALUE
  return new Intl.NumberFormat(locale, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
}

export function formatLatency(value: number | null, locale = 'en-US'): string {
  if (value === null) return EMPTY_ANALYTICS_VALUE
  if (value < 1000) {
    return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)} ms`
  }
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value / 1000)} s`
}

export function formatRatio(value: number | null, locale = 'en-US'): string {
  if (value === null) return EMPTY_ANALYTICS_VALUE
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  }).format(value)
}
