type MessageWithTimestamp = Record<string, unknown> & { created_at?: unknown }

export function normalizeMessageCreatedAt(value: unknown): string {
  if (typeof value !== 'string' || !value.trim()) return ''

  const trimmed = value.trim()
  const date = new Date(trimmed)
  if (Number.isNaN(date.getTime())) return ''
  return trimmed
}

export function applyMessageCreatedAt<T extends MessageWithTimestamp>(
  message: T,
  candidate: unknown,
): T {
  const next = normalizeMessageCreatedAt(candidate)
  if (next) message.created_at = next
  return message
}

export function ensureMessageCreatedAt<T extends MessageWithTimestamp>(
  message: T,
  fallback = new Date().toISOString(),
): T {
  if (!normalizeMessageCreatedAt(message.created_at)) {
    message.created_at = fallback
  }
  return message
}

export function bindServerTurnTimestamps(
  messages: MessageWithTimestamp[],
  payload: Record<string, unknown> | undefined,
  assistant?: MessageWithTimestamp,
): void {
  if (!payload) return

  if (assistant) {
    applyMessageCreatedAt(assistant, payload.assistant_created_at ?? payload.created_at)
  }

  const userCreatedAt = payload.user_created_at
  const userMessageId = typeof payload.user_message_id === 'string' ? payload.user_message_id : ''
  if (!normalizeMessageCreatedAt(userCreatedAt) && !userMessageId) return

  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i]
    if (message.role !== 'user') continue
    applyMessageCreatedAt(message, userCreatedAt)
    if (userMessageId && (typeof message.id !== 'string' || !message.id)) {
      message.id = userMessageId
    }
    break
  }
}

export function formatMessageTimestamp(value: unknown): string {
  const normalized = normalizeMessageCreatedAt(value)
  if (!normalized) return ''

  const date = new Date(normalized)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
