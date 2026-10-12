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
 *
 * Deleted artifacts are dropped but still consume their index: the server keeps
 * them in the list precisely so the files after them keep their download
 * address, and renumbering here would undo that.
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
      if (art.deleted_at) continue
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

/**
 * Marks an artifact deleted in the loaded history so the panel drops it without
 * a refetch. Returns whether anything changed.
 *
 * The entry is flagged rather than spliced out, for the same reason the server
 * keeps a tombstone: its position is the download address of the file, and
 * removing it would shift every later artifact of that message.
 */
export function markSessionArtifactDeleted(
  messages: unknown,
  messageId: string,
  index: number,
): boolean {
  if (!Array.isArray(messages) || !messageId) return false
  for (const raw of messages) {
    const message = asRecord(raw)
    if (!message || readMessageId(message) !== messageId) continue
    const list = Array.isArray(message.artifacts) ? message.artifacts : []
    const art = asRecord(list[index])
    if (!art || art.deleted_at) continue
    art.deleted_at = new Date().toISOString()
    return true
  }
  return false
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
