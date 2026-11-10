import { get, post } from '@/utils/request'

export interface MessageSuggestionItem {
  id: string
  text: string
  category?: 'clarify' | 'deepen' | 'action'
  source: 'model' | 'faq' | 'document' | 'wiki' | string
  knowledge_base_ids?: string[]
}

export interface MessageSuggestionSet {
  id: string
  session_id: string
  assistant_message_id: string
  status: 'generating' | 'ready' | 'suppressed' | 'failed'
  allow_regenerate: boolean
  suppression_reason?: string
  questions: MessageSuggestionItem[]
  generated_at?: string
}

export function ensureMessageSuggestions(sessionId: string, messageId: string, regenerate = false) {
  return post<{ data: MessageSuggestionSet }>(
    `/api/v1/sessions/${sessionId}/messages/${messageId}/suggestions`,
    { regenerate },
  )
}

export function getMessageSuggestions(sessionId: string, messageId: string) {
  return get<{ data: MessageSuggestionSet }>(
    `/api/v1/sessions/${sessionId}/messages/${messageId}/suggestions`,
  )
}

export function recordMessageSuggestionEvent(
  sessionId: string,
  suggestionSetId: string,
  eventType: 'impression' | 'click' | 'dismiss',
  questionId = '',
) {
  return post(
    `/api/v1/sessions/${sessionId}/suggestion-events`,
    { suggestion_set_id: suggestionSetId, question_id: questionId, event_type: eventType },
  )
}
