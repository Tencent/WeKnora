export interface SessionActivity {
  messageId: string
  detached: boolean
  failures: number
}

interface ActivityMessage {
  id: string
  role: string
  is_completed: boolean
}

interface PersistedSessionActivity {
  running: Record<string, string>
  unread: string[]
}

const MAX_PERSISTED_UNREAD = 100

// A reload drops every live stream, so whatever was running is saved as
// detached: polling then decides whether it finished while the page was gone.
export function serializeSessionActivity(
  entries: Record<string, SessionActivity>,
  unread: Record<string, true>,
): string {
  const running: Record<string, string> = {}
  for (const [sessionId, entry] of Object.entries(entries)) running[sessionId] = entry.messageId
  const data: PersistedSessionActivity = { running, unread: Object.keys(unread).slice(-MAX_PERSISTED_UNREAD) }
  return JSON.stringify(data)
}

export function restoreSessionActivity(
  raw: string | null,
  entries: Record<string, SessionActivity>,
  unread: Record<string, true>,
) {
  if (!raw) return
  let data: Partial<PersistedSessionActivity>
  try {
    data = JSON.parse(raw)
  } catch {
    return
  }
  if (data.running && typeof data.running === 'object') {
    for (const [sessionId, messageId] of Object.entries(data.running)) {
      if (sessionId && !entries[sessionId]) {
        entries[sessionId] = { messageId: typeof messageId === 'string' ? messageId : '', detached: true, failures: 0 }
      }
    }
  }
  if (Array.isArray(data.unread)) {
    for (const sessionId of data.unread) {
      if (typeof sessionId === 'string' && sessionId) unread[sessionId] = true
    }
  }
}

// Entries are replaced on every local update so stale polling responses cannot
// clear a newer turn or a stream that has since reattached.
export function createSessionActivityState(
  entries: Record<string, SessionActivity>,
  fetchMessages: (sessionId: string) => Promise<ActivityMessage[]>,
  unread: Record<string, true> = {},
) {
  const pending = new Set<string>()
  let viewingSessionId = ''

  function update(sessionId: string, running: boolean, messageId = '') {
    if (!sessionId) return
    if (running) {
      entries[sessionId] = { messageId, detached: false, failures: 0 }
      delete unread[sessionId]
    } else {
      delete entries[sessionId]
    }
  }

  function detach(sessionId: string) {
    const entry = entries[sessionId]
    if (entry) entries[sessionId] = { ...entry, detached: true }
  }

  function setViewing(sessionId: string) {
    viewingSessionId = sessionId
    if (sessionId) delete unread[sessionId]
  }

  function dismiss(sessionId: string) {
    delete entries[sessionId]
    delete unread[sessionId]
  }

  async function refresh() {
    await Promise.all(Object.entries(entries).map(async ([sessionId, entry]) => {
      if (!entry.detached || pending.has(sessionId)) return
      pending.add(sessionId)
      try {
        const messages = await fetchMessages(sessionId)
        if (entries[sessionId] !== entry) return
        const message = entry.messageId
          ? messages.find(message => message.id === entry.messageId)
          : messages.find(message => message.role === 'assistant' && !message.is_completed)
        if (message?.is_completed) {
          delete entries[sessionId]
          if (sessionId !== viewingSessionId) unread[sessionId] = true
        } else if (!message && entry.messageId) {
          delete entries[sessionId]
        } else if (message) {
          entries[sessionId] = { messageId: message.id, detached: true, failures: 0 }
        } else if (++entry.failures >= 3) {
          // A request abandoned before creating its assistant message.
          delete entries[sessionId]
        }
      } catch (error) {
        if (entries[sessionId] !== entry) return
        const status = (error as { status?: number })?.status
        if (status === 403 || status === 404 || ++entry.failures >= 3) {
          delete entries[sessionId]
        }
      } finally {
        pending.delete(sessionId)
      }
    }))
  }

  function clear() {
    for (const sessionId of Object.keys(entries)) delete entries[sessionId]
    for (const sessionId of Object.keys(unread)) delete unread[sessionId]
  }

  return { update, detach, setViewing, dismiss, refresh, clear }
}
