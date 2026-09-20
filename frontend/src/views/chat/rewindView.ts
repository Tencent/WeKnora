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
  if (opts.embeddedMode || opts.outgoingWork || !messageId) {
    return { canRewind: false }
  }
  const index = messages.findIndex((m) => String(m.id || '') === messageId)
  if (index < 0) {
    return { canRewind: false }
  }
  if (messages.some((m) => m.role === 'assistant' && m.is_completed === false)) {
    return { canRewind: false }
  }
  const target = messages[index]
  if (target.role === 'assistant' || target.role === 'user') {
    return { canRewind: true }
  }
  return { canRewind: false }
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
  return 'chat.rewind.busy'
}
