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
