/**
 * Fork affordance rules, kept as a pure function so they can be tested without
 * mounting the chat view.
 *
 * The backend re-derives all of this; this is purely so the UI can show the
 * right button state without an extra round trip.
 *
 * User messages fork *at* the question (history excludes it, composer prefills
 * it). Assistant messages fork *after* the answer (history includes it).
 */

export interface ForkCandidateMessage {
  id?: unknown
  role?: unknown
  is_completed?: unknown
}

export interface ForkAffordance {
  /** Whether the fork button should be offered on this message at all. */
  canFork: boolean
}

const REFUSED: ForkAffordance = { canFork: false }

export function resolveForkAffordance(
  messages: ForkCandidateMessage[],
  messageId: string,
): ForkAffordance {
  const index = messages.findIndex((m) => m.id === messageId)
  if (index < 0) {
    return REFUSED
  }

  // A turn still streaming means the source session holds an active sandbox
  // lease. The backend would answer 409, so do not offer the button.
  if (messages.some((m) => m.role === 'assistant' && m.is_completed === false)) {
    return REFUSED
  }

  const target = messages[index]
  if (target.role === 'assistant' || target.role === 'user') {
    return { canFork: true }
  }
  return REFUSED
}
