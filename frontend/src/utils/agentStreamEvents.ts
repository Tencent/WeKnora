export type AgentStreamEventLike = {
  type?: string
  [key: string]: unknown
}

const THINKING_PROMOTION_BLOCKERS = new Set([
  'tool_call',
  'tool_result',
  'tool_approval_required',
  'tool_approval_resolved',
  'error',
])

export function hasAgentToolActivity(stream: AgentStreamEventLike[] | null | undefined): boolean {
  if (!Array.isArray(stream)) return false

  return stream.some((event) => {
    if (!event || typeof event !== 'object') return false
    return THINKING_PROMOTION_BLOCKERS.has(event.type || '')
  })
}

export function canPromoteTrailingThinkingToAnswer(stream: AgentStreamEventLike[] | null | undefined): boolean {
  return !hasAgentToolActivity(stream)
}
