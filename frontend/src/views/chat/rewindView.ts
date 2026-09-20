/**
 * Client-side rewind helpers. The HTTP call mutates the source session on
 * the server; these decide whether that result may touch the live chat view.
 */

export function shouldApplyRewindLocally(currentSessionId: string, sourceSessionId: string): boolean {
  return Boolean(currentSessionId) && currentSessionId === sourceSessionId
}

export function rewindPrefillText(role: unknown, content: unknown): string {
  if (role !== 'user') {
    return ''
  }
  return String(content ?? '')
}

export function rewindBlockedByOutgoingWork(state: {
  isReplying?: boolean
  isStreaming?: boolean
  isRecovering?: boolean
}): boolean {
  return Boolean(state.isReplying || state.isStreaming || state.isRecovering)
}

/** Empty history is success (the conversation was cleared). A thrown reload is not. */
export function canReplaceRewindTranscript(
  currentSessionId: string,
  sourceSessionId: string,
  reloadError: unknown,
): boolean {
  if (reloadError) {
    return false
  }
  return shouldApplyRewindLocally(currentSessionId, sourceSessionId)
}

export function rewindHistoryHasMore(batchLength: number, limit: number): boolean {
  return batchLength >= limit
}

export function keepMessagesThroughRewindPoint<T extends { id?: unknown }>(
  messages: T[],
  messageId: string,
  role: unknown,
  isMatch: (message: T) => boolean = (message) => String(message.id || '') === messageId,
): T[] {
  const index = messages.findIndex(isMatch)
  if (index < 0) {
    return messages
  }
  if (role === 'user') {
    return messages.slice(0, index)
  }
  return messages.slice(0, index + 1)
}

export interface RewindCandidateMessage {
  id?: unknown
  role?: unknown
  is_completed?: unknown
}

export function resolveRewindAffordance(
  messages: RewindCandidateMessage[],
  messageId: string,
  opts: { embeddedMode?: boolean; outgoingWork?: boolean } = {},
): { canRewind: boolean } {
  return { canRewind: rewindableMessageIds(messages, opts).has(messageId) }
}

/**
 * The ids a rewind control may be offered on, resolved in one pass.
 *
 * The transcript asks this per rendered message and re-asks on every streamed
 * token, so the whole-list conditions (is any turn still generating?) are
 * hoisted out of the per-message check instead of rescanning for each row.
 */
export function rewindableMessageIds(
  messages: RewindCandidateMessage[],
  opts: { embeddedMode?: boolean; outgoingWork?: boolean } = {},
): Set<string> {
  const ids = new Set<string>()
  if (opts.embeddedMode || opts.outgoingWork) {
    return ids
  }
  for (const message of messages) {
    if (message.role === 'assistant' && message.is_completed === false) {
      return new Set<string>()
    }
    const id = String(message.id || '')
    if (id && (message.role === 'assistant' || message.role === 'user')) {
      ids.add(id)
    }
  }
  return ids
}

export function rewindHttpConflictCode(err: unknown): string {
  if (!err || typeof err !== 'object') {
    return ''
  }
  const rec = err as { code?: unknown; status?: unknown; $httpStatus?: unknown }
  const status = rec.status ?? rec.$httpStatus
  if (status !== 409) {
    return ''
  }
  return typeof rec.code === 'string' ? rec.code : ''
}

export function rewindConflictI18nKey(code: string): string {
  if (code === 'REWIND_NO_CHECKPOINT') {
    return 'chat.rewind.noCheckpoint'
  }
  if (code === 'REWIND_SANDBOX_REPLACED') {
    return 'chat.rewind.sandboxReplaced'
  }
  return 'chat.rewind.busy'
}
