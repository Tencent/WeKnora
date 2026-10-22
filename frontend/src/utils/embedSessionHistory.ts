/** Browser-local signed handles only; messages and API tokens stay out of storage. */
export interface EmbedHistoryScope {
  channelId: string
  agentId: string
  visitorId: string
}

export interface EmbedHistoryEntry {
  id: string
  sig: string
  title: string
  updatedAt: number
}

export const EMBED_HISTORY_LIMIT = 30
type HistoryStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

export function embedHistoryStorageKey(scope: EmbedHistoryScope): string {
  return `weknora-embed-history:${JSON.stringify([scope.channelId, scope.agentId, scope.visitorId])}`
}

function browserStorage(): HistoryStorage | undefined {
  try { return typeof window === 'undefined' ? undefined : window.localStorage } catch { return undefined }
}

function normalize(entries: unknown): EmbedHistoryEntry[] {
  if (!Array.isArray(entries)) return []
  const seen = new Set<string>()
  return entries.filter((entry): entry is EmbedHistoryEntry => (
    entry && typeof entry.id === 'string' && entry.id.trim() && entry.id.length <= 256
    && typeof entry.sig === 'string' && entry.sig.length <= 2048
    && typeof entry.updatedAt === 'number' && Number.isFinite(entry.updatedAt) && entry.updatedAt >= 0
    && entry.updatedAt <= 8.64e15
  )).map(entry => ({
    id: entry.id.trim(), sig: entry.sig,
    title: typeof entry.title === 'string' ? entry.title.trim().slice(0, 160) : '',
    updatedAt: entry.updatedAt,
  })).sort((a, b) => b.updatedAt - a.updatedAt).filter(entry => {
    if (seen.has(entry.id)) return false
    seen.add(entry.id)
    return true
  }).slice(0, EMBED_HISTORY_LIMIT)
}

export function readEmbedHistory(scope: EmbedHistoryScope, storage = browserStorage()): EmbedHistoryEntry[] {
  try { return normalize(JSON.parse(storage?.getItem(embedHistoryStorageKey(scope)) || '[]')) } catch { return [] }
}

export function saveEmbedHistory(
  scope: EmbedHistoryScope, entries: EmbedHistoryEntry[], storage = browserStorage(),
): EmbedHistoryEntry[] {
  const normalized = normalize(entries)
  try {
    if (normalized.length) storage?.setItem(embedHistoryStorageKey(scope), JSON.stringify(normalized))
    else storage?.removeItem(embedHistoryStorageKey(scope))
  } catch { /* Keep this page usable when storage is full or disabled. */ }
  return normalized
}

export function rememberEmbedSession(
  scope: EmbedHistoryScope, entry: EmbedHistoryEntry, previous: EmbedHistoryEntry[], storage = browserStorage(),
): EmbedHistoryEntry[] {
  const others = [...previous, ...readEmbedHistory(scope, storage)].filter(item => item.id !== entry.id)
  return saveEmbedHistory(scope, [entry, ...others], storage)
}

// A durable clear marker lets suspended tabs detect a clear before writing again.
export function embedHistoryRevisionKey(scope: EmbedHistoryScope): string {
  return `${embedHistoryStorageKey(scope)}:revision`
}

export function readEmbedHistoryRevision(scope: EmbedHistoryScope, storage = browserStorage()): string {
  try { return storage?.getItem(embedHistoryRevisionKey(scope)) || '' } catch { return '' }
}

export function clearEmbedHistory(scope: EmbedHistoryScope, storage = browserStorage()): EmbedHistoryEntry[] {
  try {
    storage?.setItem(embedHistoryRevisionKey(scope), `${Date.now()}:${Math.random()}`)
  } catch { /* Storage is best-effort, just like saved handles. */ }
  return saveEmbedHistory(scope, [], storage)
}
