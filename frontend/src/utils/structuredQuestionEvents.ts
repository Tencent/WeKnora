type StreamEvent = Record<string, any>

export interface PendingUserInputSnapshot {
  pending_id: string
  assistant_message_id: string
  question: Record<string, any>
}

export function pendingSnapshotToEvent(snapshot: PendingUserInputSnapshot): StreamEvent {
  return {
    type: 'user_input_required',
    pending_id: snapshot.pending_id,
    assistant_message_id: snapshot.assistant_message_id,
    ...snapshot.question,
    resolved: false,
  }
}

function parseRecord(value: unknown): Record<string, any> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, any>
  }
  if (typeof value !== 'string' || !value.trim()) return {}
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

function parseOptions(value: unknown): Record<string, any>[] {
  if (Array.isArray(value)) return value
  if (typeof value !== 'string' || !value.trim()) return []
  try {
    const parsed = JSON.parse(value)
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function toBoolean(value: unknown, fallback: boolean): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') {
    if (value.toLowerCase() === 'true') return true
    if (value.toLowerCase() === 'false') return false
  }
  return fallback
}

function toOptionalNumber(value: unknown): number | undefined {
  const number = Number(value)
  return Number.isFinite(number) ? number : undefined
}

// Persisted agent history only contains tool steps. Rebuild ask_user steps as
// terminal cards so history matches the live SSE experience without resubmission.
export function historicalAskUserEvent(toolCall: Record<string, any>): StreamEvent | null {
  if (toolCall?.name !== 'ask_user') return null

  const args = parseRecord(toolCall.args)
  const result = parseRecord(toolCall.result)
  const output = parseRecord(result.output)
  const data = parseRecord(result.data)
  const answer = { ...output, ...data }
  const callId = String(toolCall.id || `${args.question_group_id || 'question'}-${args.question_index || 1}`)

  return {
    type: 'user_input_required',
    pending_id: `history-${callId}`,
    question: String(args.question || ''),
    mode: String(args.mode || 'text'),
    field_key: args.field_key,
    question_group_id: args.question_group_id || answer.question_group_id,
    question_index: toOptionalNumber(args.question_index ?? answer.question_index),
    question_total: toOptionalNumber(args.question_total ?? answer.question_total),
    completed_count: toOptionalNumber(args.completed_count ?? answer.completed_count),
    remaining_count: toOptionalNumber(args.remaining_count ?? answer.remaining_count),
    options: parseOptions(args.options),
    allow_other: toBoolean(args.allow_other, false),
    allow_skip: toBoolean(args.allow_skip, true),
    resolved: true,
    status: String(answer.status || (result.success === false ? 'canceled' : 'answered')),
    selected_options: Array.isArray(answer.selected_options) ? answer.selected_options : [],
    value: answer.value,
    other_text: String(answer.other_text || ''),
    reason: String(answer.reason || result.error || ''),
  }
}

// reconcileStructuredQuestionEvents folds required/resolved pairs into one
// stable card while leaving unrelated stream event objects untouched.
export function reconcileStructuredQuestionEvents(events: StreamEvent[]): StreamEvent[] {
  const result: StreamEvent[] = []
  const questionIndex = new Map<string, number>()

  for (const event of events) {
    if (!event || typeof event !== 'object') continue
    const pendingId = typeof event.pending_id === 'string' ? event.pending_id : ''
    if (event.type === 'user_input_required' && pendingId) {
      const existingIndex = questionIndex.get(pendingId)
      if (existingIndex === undefined) {
        questionIndex.set(pendingId, result.length)
        result.push({ ...event, resolved: Boolean(event.resolved) })
      } else {
        const existing = result[existingIndex]
        result[existingIndex] = {
          ...existing,
          ...event,
          resolved: Boolean(existing.resolved || event.resolved),
        }
      }
      continue
    }
    if (event.type === 'user_input_resolved' && pendingId) {
      const existingIndex = questionIndex.get(pendingId)
      if (existingIndex !== undefined) {
        result[existingIndex] = {
          ...result[existingIndex],
          resolved: true,
          status: event.status,
          selected_options: event.selected_options || [],
          value: event.value,
          other_text: event.other_text || '',
          reason: event.reason || '',
        }
      }
      continue
    }
    result.push(event)
  }
  return result
}
