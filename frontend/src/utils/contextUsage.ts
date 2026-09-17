export type ContextUsage = {
  system_prompt?: number
  tools?: number
  conversation?: number
  mcp?: number
  skills?: number
  total?: number
  window?: number
}

export const CONTEXT_USAGE_CATEGORIES = [
  { key: 'system_prompt', color: '#3b82f6' },
  { key: 'tools', color: '#22c55e' },
  { key: 'conversation', color: '#f59e0b' },
  { key: 'mcp', color: '#8b5cf6' },
  { key: 'skills', color: '#ec4899' },
] as const

export type ContextUsageCategoryKey = typeof CONTEXT_USAGE_CATEGORIES[number]['key']

type MessageWithUsage = {
  role?: string
  content?: string
  is_completed?: boolean
  usage?: {
    context?: ContextUsage
  }
}

function hasContextSnapshot(context?: ContextUsage): context is ContextUsage {
  return !!context && ((context.total ?? 0) > 0 || (context.window ?? 0) > 0)
}

export function latestContextUsage(messages: MessageWithUsage[] | null | undefined): ContextUsage | null {
  if (!messages?.length) {
    return null
  }
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i]
    if (message?.role !== 'assistant') {
      continue
    }
    const context = message.usage?.context
    if (hasContextSnapshot(context)) {
      return context
    }
    // Still streaming and this turn has no snapshot yet: keep the previous agent one.
    if (message.is_completed === false) {
      continue
    }
    // A finished non-agent reply (quick answer / RAG) must not inherit
    // an older agent snapshot.
    return null
  }
  return null
}

/** 213200 → 213.2K, 1000000 → 1000.0K */
export function formatContextUsageCount(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens < 0) {
    return '0'
  }
  if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1)}K`
  }
  return String(Math.round(tokens))
}

export function contextUsagePercent(usage?: ContextUsage | null): number {
  const window = usage?.window ?? 0
  const total = usage?.total ?? 0
  if (window <= 0) {
    return 0
  }
  return Math.min(100, Math.max(0, (total / window) * 100))
}

export function contextUsageCategoryTokens(usage: ContextUsage | null | undefined, key: ContextUsageCategoryKey): number {
  return usage?.[key] ?? 0
}

export function contextUsageBarSegments(usage?: ContextUsage | null): Array<{
  key: ContextUsageCategoryKey
  color: string
  tokens: number
  percent: number
}> {
  const window = usage?.window ?? 0
  if (window <= 0) {
    return []
  }
  const segs = CONTEXT_USAGE_CATEGORIES
    .map((row) => {
      const tokens = contextUsageCategoryTokens(usage, row.key)
      return {
        key: row.key,
        color: row.color,
        tokens,
        percent: (tokens / window) * 100,
      }
    })
    .filter((row) => row.tokens > 0)
  const filled = segs.reduce((sum, row) => sum + row.percent, 0)
  if (filled <= 100) {
    return segs
  }
  return segs.map((row) => ({ ...row, percent: row.percent * (100 / filled) }))
}
