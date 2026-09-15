import type { ArtifactMeta } from '@/api/chat'
import { persistedAssistantId } from './steerStreamFork'

/** Artifact metadata plus the assistant message that owns the download index. */
export type SessionArtifactItem = ArtifactMeta & {
  messageId: string
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function readMessageId(message: Record<string, unknown>): string {
  return persistedAssistantId(message) || String(message.request_id || '')
}

/**
 * Flatten every assistant message's artifacts in list order. Download still
 * uses the per-message index (not a session-wide offset), so each row keeps
 * the owning message id.
 */
export function collectSessionArtifacts(messages: unknown): SessionArtifactItem[] {
  if (!Array.isArray(messages)) return []
  const items: SessionArtifactItem[] = []
  for (const raw of messages) {
    const message = asRecord(raw)
    if (!message) continue
    const list = Array.isArray(message.artifacts) ? message.artifacts : []
    if (!list.length) continue
    const messageId = readMessageId(message)
    if (!messageId) continue
    for (let i = 0; i < list.length; i++) {
      const art = asRecord(list[i])
      if (!art) continue
      const index = Number.isInteger(art.index) ? (art.index as number) : i
      items.push({
        ...(art as unknown as ArtifactMeta),
        index,
        messageId,
      })
    }
  }
  return items
}

export function formatArtifactSize(size: number | undefined | null): string {
  if (!size || size < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = size
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return unit === 0 ? `${value} ${units[unit]}` : `${value.toFixed(1)} ${units[unit]}`
}

export function formatArtifactDateTime(raw: string | undefined | null): string {
  if (!raw) return '—'
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return String(raw)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())} ${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`
}
