export type StreamRecoveryMessage = Record<string, unknown>

/** Mark only the current incomplete assistant row as reconnecting after reload. */
export function markLatestIncompleteAssistantForRecovery(
  messages: StreamRecoveryMessage[],
): StreamRecoveryMessage | undefined {
  const last = messages[messages.length - 1]
  if (last?.role !== 'assistant' || last.is_completed === true) return undefined

  last.isRecoveringStream = true
  return last
}

/** Clear the temporary reconnecting UI before normal stream rendering resumes. */
export function clearMessageStreamRecovery(
  message: StreamRecoveryMessage | undefined,
): void {
  if (message?.isRecoveringStream) {
    message.isRecoveringStream = false
  }
}
