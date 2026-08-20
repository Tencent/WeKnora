type MessageWithTimestamp = Record<string, unknown> & { created_at?: unknown }

export function ensureMessageCreatedAt<T extends MessageWithTimestamp>(
  message: T,
  fallback = new Date().toISOString(),
): T {
  if (typeof message.created_at !== 'string' || !message.created_at.trim()) {
    message.created_at = fallback
  }
  return message
}

export function formatMessageTimestamp(value: unknown): string {
  if (typeof value !== 'string' || !value.trim()) return ''

  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''

  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
