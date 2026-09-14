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

/** Insert a flow divider after this much idle time, or when the calendar day changes. */
export const CONVERSATION_TIMESTAMP_GAP_MS = 5 * 60 * 1000

export type ConversationTimestampKind = 'today' | 'yesterday' | 'thisYear' | 'otherYear'

export type ConversationTimestampModel = {
  datetime: string
  kind: ConversationTimestampKind
  time: string
  year: number
  month: number
  day: number
}

type ConversationTimestampMessage = {
  role?: unknown
  created_at?: unknown
}

function startOfLocalDay(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
}

function formatClock(date: Date): string {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function getConversationTimestampKind(date: Date, now = new Date()): ConversationTimestampKind {
  const day = startOfLocalDay(date)
  const today = startOfLocalDay(now)
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1).getTime()

  if (day === today) return 'today'
  if (day === yesterday) return 'yesterday'
  if (date.getFullYear() === now.getFullYear()) return 'thisYear'
  return 'otherYear'
}

export function getConversationTimestampModel(
  value: unknown,
  now = new Date(),
): ConversationTimestampModel | null {
  const datetime = normalizeMessageCreatedAt(value)
  if (!datetime) return null

  const date = new Date(datetime)
  return {
    datetime,
    kind: getConversationTimestampKind(date, now),
    time: formatClock(date),
    year: date.getFullYear(),
    month: date.getMonth() + 1,
    day: date.getDate(),
  }
}

export function shouldInsertConversationTimestamp(
  previous: ConversationTimestampMessage | undefined,
  current: ConversationTimestampMessage | undefined,
  gapMs = CONVERSATION_TIMESTAMP_GAP_MS,
): boolean {
  if (!current || !normalizeMessageCreatedAt(current.created_at)) return false
  if (current.role === 'assistant' && previous?.role === 'user') return false

  const previousCreatedAt = normalizeMessageCreatedAt(previous?.created_at)
  if (!previousCreatedAt) return true

  const previousDate = new Date(previousCreatedAt)
  const currentDate = new Date(current.created_at as string)
  if (startOfLocalDay(previousDate) !== startOfLocalDay(currentDate)) return true
  return currentDate.getTime() - previousDate.getTime() >= gapMs
}

export function shouldShowConversationTimestamp(
  messages: ConversationTimestampMessage[],
  index: number,
  gapMs = CONVERSATION_TIMESTAMP_GAP_MS,
): boolean {
  return shouldInsertConversationTimestamp(messages[index - 1], messages[index], gapMs)
}
