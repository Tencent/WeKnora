export type ChatFeedbackValue = boolean | null

export interface ChatFeedbackIntent {
  value: ChatFeedbackValue
  dislikeReason?: string
}

interface QueuedChatFeedbackIntent extends ChatFeedbackIntent {
  messageId: string
}

export interface ChatFeedbackStateControllerOptions {
  read: (messageId: string) => Promise<ChatFeedbackValue>
  submit: (messageId: string, isPositive: boolean, dislikeReason?: string) => Promise<void>
  cancel: (messageId: string) => Promise<void>
  onValueChange: (value: ChatFeedbackValue) => void
  onPendingChange: (pending: boolean) => void
  onMutationSuccess?: (intent: ChatFeedbackIntent) => void
  onMutationError?: (error: unknown, intent: ChatFeedbackIntent) => void
  onLoadError?: (error: unknown) => void
}

export interface ChatFeedbackStateController {
  setMessage: (messageId: string, initialValue?: ChatFeedbackValue) => void
  load: () => Promise<void>
  request: (intent: ChatFeedbackIntent) => Promise<void>
  dispose: () => void
}

function isSameIntent(
  left: QueuedChatFeedbackIntent | null,
  right: QueuedChatFeedbackIntent | null,
): boolean {
  return Boolean(
    left &&
      right &&
      left.messageId === right.messageId &&
      left.value === right.value &&
      (left.dislikeReason || '') === (right.dislikeReason || ''),
  )
}

/**
 * Coordinates one message's feedback read and mutations.
 *
 * Mutations are serialized and an intent queued while another request is in
 * flight replaces the older queued intent. A read started before a mutation is
 * invalidated, so it cannot restore stale state after the write succeeds.
 */
export function createChatFeedbackStateController(
  options: ChatFeedbackStateControllerOptions,
): ChatFeedbackStateController {
  let activeMessageId = ''
  let loadRevision = 0
  let desiredIntent: QueuedChatFeedbackIntent | null = null
  let queuedIntent: QueuedChatFeedbackIntent | null = null
  let inFlightIntent: QueuedChatFeedbackIntent | null = null
  let runner: Promise<void> | null = null
  let pending = false
  let disposed = false

  const setPending = (next: boolean) => {
    if (pending === next) return
    pending = next
    if (!disposed) options.onPendingChange(next)
  }

  const isCurrentFinalIntent = (intent: QueuedChatFeedbackIntent) =>
    !disposed &&
    activeMessageId === intent.messageId &&
    !queuedIntent &&
    isSameIntent(desiredIntent, intent)

  const toPublicIntent = (intent: QueuedChatFeedbackIntent): ChatFeedbackIntent => ({
    value: intent.value,
    dislikeReason: intent.dislikeReason,
  })

  const recoverAuthoritativeValue = async (intent: QueuedChatFeedbackIntent) => {
    try {
      const value = await options.read(intent.messageId)
      if (isCurrentFinalIntent(intent)) options.onValueChange(value)
    } catch {
      // The mutation error is already surfaced. Keep the last confirmed local
      // value when the recovery read also fails.
    }
  }

  const drain = async () => {
    while (queuedIntent && !disposed) {
      const intent = queuedIntent
      queuedIntent = null
      inFlightIntent = intent

      try {
        if (intent.value === null) {
          await options.cancel(intent.messageId)
        } else {
          await options.submit(intent.messageId, intent.value, intent.dislikeReason)
        }

        if (isCurrentFinalIntent(intent)) {
          options.onValueChange(intent.value)
          options.onMutationSuccess?.(toPublicIntent(intent))
          if (isCurrentFinalIntent(intent)) desiredIntent = null
        }
      } catch (error) {
        if (isCurrentFinalIntent(intent)) {
          options.onMutationError?.(error, toPublicIntent(intent))
          await recoverAuthoritativeValue(intent)
          if (isCurrentFinalIntent(intent)) desiredIntent = null
        }
      } finally {
        inFlightIntent = null
      }
    }

    setPending(false)
  }

  const ensureRunner = (): Promise<void> => {
    if (runner) return runner
    const current = drain()
    runner = current
    void current.then(() => {
      if (runner === current) runner = null
    })
    return current
  }

  return {
    setMessage(messageId: string, initialValue?: ChatFeedbackValue) {
      if (disposed) return
      if (messageId === activeMessageId) {
        if (
          initialValue !== undefined &&
          !desiredIntent &&
          !inFlightIntent &&
          !queuedIntent
        ) {
          options.onValueChange(initialValue)
        }
        return
      }
      activeMessageId = messageId
      loadRevision += 1
      desiredIntent = null
      queuedIntent = null
      setPending(false)
      options.onValueChange(initialValue === undefined ? null : initialValue)
    },

    async load() {
      const messageId = activeMessageId
      if (disposed || !messageId) return

      const revision = ++loadRevision
      try {
        const value = await options.read(messageId)
        if (
          !disposed &&
          activeMessageId === messageId &&
          revision === loadRevision &&
          !desiredIntent
        ) {
          options.onValueChange(value)
        }
      } catch (error) {
        if (!disposed && activeMessageId === messageId && revision === loadRevision) {
          options.onLoadError?.(error)
        }
      }
    },

    request(intent: ChatFeedbackIntent) {
      if (disposed || !activeMessageId) return Promise.resolve()

      loadRevision += 1
      const next: QueuedChatFeedbackIntent = {
        messageId: activeMessageId,
        value: intent.value,
        dislikeReason: intent.dislikeReason,
      }
      desiredIntent = next

      if (inFlightIntent && isSameIntent(inFlightIntent, next)) {
        // A newer intent returned to the state already being written. Drop any
        // superseded queued state and let the active request become final.
        queuedIntent = null
      } else {
        // Last-intent wins: retain at most one follow-up mutation.
        queuedIntent = next
      }

      setPending(true)
      return ensureRunner()
    },

    dispose() {
      disposed = true
      activeMessageId = ''
      loadRevision += 1
      desiredIntent = null
      queuedIntent = null
    },
  }
}
