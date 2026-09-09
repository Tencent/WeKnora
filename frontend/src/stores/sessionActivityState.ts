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

// Entries are replaced on every local update so stale polling responses cannot
// clear a newer turn or a stream that has since reattached.
export function createSessionActivityState(
  entries: Record<string, SessionActivity>,
  fetchMessages: (sessionId: string) => Promise<ActivityMessage[]>,
) {
  const pending = new Set<string>()

  function update(sessionId: string, running: boolean, messageId = '') {
    if (!sessionId) return
    if (running) entries[sessionId] = { messageId, detached: false, failures: 0 }
    else delete entries[sessionId]
  }

  function detach(sessionId: string) {
    const entry = entries[sessionId]
    if (entry) entries[sessionId] = { ...entry, detached: true }
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
        if (message?.is_completed || (!message && entry.messageId)) {
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
  }

  return { update, detach, refresh, clear }
}
