/**
 * Fork affordance rules, kept as a pure function so they can be tested without
 * mounting the chat view.
 *
 * The backend re-derives all of this; this is purely so the UI can show the
 * right button state and tooltip without an extra round trip.
 */

export interface ForkCandidateMessage {
  id?: unknown
  role?: unknown
  is_completed?: unknown
  sandbox_checkpoint?: unknown
}

export interface ForkAffordance {
  /** Whether the fork button should be offered on this message at all. */
  canFork: boolean
  /**
   * Whether forking here will start from a brand new sandbox rather than a
   * copy of the current one. The fork still succeeds; the tooltip just says so.
   */
  willDegrade: boolean
}

const REFUSED: ForkAffordance = { canFork: false, willDegrade: false }

export function resolveForkAffordance(
  messages: ForkCandidateMessage[],
  messageId: string,
): ForkAffordance {
  const index = messages.findIndex((m) => m.id === messageId)
  if (index < 0 || messages[index].role !== 'user') {
    return REFUSED
  }

  // A turn still streaming means the source session holds an active sandbox
  // lease. The backend would answer 409, so do not offer the button.
  if (messages.some((m) => m.role === 'assistant' && m.is_completed === false)) {
    return REFUSED
  }

  // Walk back to the nearest preceding assistant message: that turn's
  // checkpoint is the state a fork here would roll back to.
  for (let i = index - 1; i >= 0; i -= 1) {
    if (messages[i].role !== 'assistant') continue
    return { canFork: true, willDegrade: !messages[i].sandbox_checkpoint }
  }

  // Nothing before this message produced output, so a brand new sandbox is the
  // correct result rather than a degradation.
  return { canFork: true, willDegrade: false }
}
