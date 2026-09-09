import { markRaw } from 'vue'

export type ChatMessage = Record<string, unknown>

/**
 * The row id the server knows this assistant message by.
 *
 * A turn that absorbed a mid-run message is shown as several assistant
 * segments, and every segment after the first carries a synthetic id so Vue
 * can key the list. Only the persisted id addresses a real message, so
 * anything that talks to the backend — continue-stream, stop, artifacts —
 * must resolve through here rather than reading `id` directly.
 */
export function persistedAssistantId(message: ChatMessage | undefined): string {
  if (!message) return ''
  if (typeof message.assistant_message_id === 'string' && message.assistant_message_id) {
    return message.assistant_message_id
  }
  return typeof message.id === 'string' ? message.id : ''
}

export function sealAssistantSegment(message: ChatMessage): void {
  message.thinking = false
  message.is_completed = true
  message.steerForked = true
  const stream = message.agentEventStream
  if (!Array.isArray(stream)) return
  for (const raw of stream) {
    if (!raw || typeof raw !== 'object') continue
    const event = raw as ChatMessage
    if (event.type === 'thinking') {
      event.done = true
      event.thinking = false
    }
  }
}

export function forkAfterInjectedUser(
  list: ChatMessage[],
  sourceAssistant: ChatMessage,
  userMessage: ChatMessage,
  steerId?: string,
): ChatMessage {
  // continue-stream replays the whole event log, so after a refresh this runs
  // again for a transcript history has already split. Redoing the fork would
  // duplicate the bubble and seal the segment that is still streaming.
  //
  // "Already split" means the bubble sits *between* two segments of this turn.
  // A bubble merely sitting above the live assistant is the opposite case —
  // it still has to be moved underneath it.
  const existingIdx = list.indexOf(userMessage)
  if (existingIdx > 0) {
    const before = list[existingIdx - 1]
    const after = list[existingIdx + 1]
    if (
      before?.role === 'assistant' &&
      before.request_id === sourceAssistant.request_id &&
      after?.role === 'assistant' &&
      !after.is_completed &&
      after.request_id === sourceAssistant.request_id
    ) {
      return after
    }
  }

  sealAssistantSegment(sourceAssistant)

  let userIdx = list.indexOf(userMessage)
  let sourceIdx = list.indexOf(sourceAssistant)
  if (userIdx < 0) {
    const insertAt = sourceIdx >= 0 ? sourceIdx + 1 : list.length
    list.splice(insertAt, 0, userMessage)
    userIdx = insertAt
  } else if (sourceIdx >= 0 && userIdx !== sourceIdx + 1) {
    list.splice(userIdx, 1)
    if (userIdx < sourceIdx) sourceIdx -= 1
    list.splice(sourceIdx + 1, 0, userMessage)
    userIdx = sourceIdx + 1
  }

  const afterUser = list[userIdx + 1]
  if (
    afterUser?.role === 'assistant' &&
    !afterUser.is_completed &&
    afterUser.request_id === sourceAssistant.request_id
  ) {
    return afterUser
  }

  const persistedId =
    (typeof sourceAssistant.assistant_message_id === 'string' && sourceAssistant.assistant_message_id) ||
    (typeof sourceAssistant.id === 'string' ? sourceAssistant.id : '')
  const continuation: ChatMessage = {
    id: `steer-cont-${steerId || String(Date.now())}`,
    assistant_message_id: persistedId,
    request_id: sourceAssistant.request_id,
    role: 'assistant',
    content: '',
    isAgentMode: true,
    isRagMode: sourceAssistant.isRagMode,
    is_completed: false,
    hideContent: true,
    agentEventStream: [],
    _eventMap: new Map(),
    _pendingToolCalls: new Map(),
    knowledge_references: [],
  }
  list.splice(userIdx + 1, 0, continuation)
  return continuation
}

function eventTime(event: ChatMessage): number {
  if (typeof event.timestamp === 'number' && Number.isFinite(event.timestamp)) {
    return event.timestamp
  }
  if (typeof event.timestamp === 'string') {
    const parsed = Date.parse(event.timestamp)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  return 0
}

function isTrailingTurnEvent(event: ChatMessage): boolean {
  if (event.type === 'agent_complete' || event.type === 'stop') return true
  return event.type === 'answer' && event.done === true && !event.superseded
}

export function expandSteerForksInHistory(messages: ChatMessage[]): ChatMessage[] {
  const out: ChatMessage[] = []
  for (let i = 0; i < messages.length; i++) {
    const item = messages[i]
    if (item.role !== 'assistant' || typeof item.request_id !== 'string' || !item.request_id) {
      out.push(item)
      continue
    }
    if (item.steerForked) {
      out.push(item)
      continue
    }
    const persisted = persistedAssistantId(item)
    if (persisted && item.id !== persisted) {
      out.push(item)
      continue
    }

    const forks: ChatMessage[] = []
    let j = i + 1
    while (
      j < messages.length &&
      messages[j].role === 'user' &&
      messages[j].request_id === item.request_id
    ) {
      forks.push(messages[j])
      j++
    }
    if (!forks.length) {
      out.push(item)
      continue
    }

    const stream = Array.isArray(item.agentEventStream) ? (item.agentEventStream as ChatMessage[]) : []
    const cuts = forks.map((user) => {
      const parsed = Date.parse(String(user.created_at || ''))
      return Number.isNaN(parsed) ? 0 : parsed
    })
    const buckets: ChatMessage[][] = Array.from({ length: forks.length + 1 }, () => [])
    const trailing: ChatMessage[] = []
    for (const event of stream) {
      if (isTrailingTurnEvent(event)) {
        trailing.push(event)
        continue
      }
      const t = eventTime(event)
      let idx = 0
      while (idx < cuts.length && t >= cuts[idx]) idx += 1
      buckets[idx].push(event)
    }
    buckets[buckets.length - 1].push(...trailing)

    const persistedId =
      (typeof item.assistant_message_id === 'string' && item.assistant_message_id) ||
      (typeof item.id === 'string' ? item.id : '')
    // Segment 0 reuses the original object, and sealing it overwrites fields
    // the later segments still need. Snapshot first so every segment is built
    // from the message as it arrived — most importantly `is_completed`: a
    // trailing segment that inherits the prefix's sealed `true` tells the chat
    // view the turn is over, so a still-running agent is never resumed.
    const original: ChatMessage = { ...item }

    for (let s = 0; s < buckets.length; s++) {
      const isLast = s === buckets.length - 1
      const segment: ChatMessage = s === 0
        ? item
        : {
            ...original,
            id: `${persistedId}:steer:${s}`,
            assistant_message_id: persistedId,
          }
      segment.agentEventStream = markRaw(buckets[s])
      if (isLast) {
        segment.content = original.content
        segment.artifacts = original.artifacts
        segment.usage = original.usage
        segment.knowledge_references = original.knowledge_references
        segment.is_completed = original.is_completed
        segment.steerForked = original.steerForked
      } else {
        segment.steerForked = true
        segment.is_completed = true
        segment.content = ''
        segment.artifacts = undefined
        segment.usage = undefined
        segment.knowledge_references = []
      }
      out.push(segment)
      if (s < forks.length) out.push(forks[s])
    }
    i = j - 1
  }
  return out
}
