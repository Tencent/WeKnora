import type { SuggestedQuestion } from '@/api/agent/index'

// The knowledge source a suggested question was generated from. Sent with the
// picked question so the agent searches that source before answering; the
// backend treats it as a hint inside the turn's search targets.
export interface QuestionOrigin {
  knowledge_base_id: string
  knowledge_id?: string
}

// Options bound to one send, carried from the composer to the page that
// issues the request. Keeping them on the send itself (rather than in shared
// "pending" state) means a send that never happens, or a second pick before
// the first one goes out, cannot attach them to the wrong question.
export interface SendMessageOptions {
  questionOrigin?: QuestionOrigin
}

// Agent-authored starters have no knowledge source and yield undefined.
export function questionOriginFromSuggestion(
  item: Pick<SuggestedQuestion, 'knowledge_base_id' | 'knowledge_id'> | null | undefined,
): QuestionOrigin | undefined {
  const knowledgeBaseId = item?.knowledge_base_id?.trim()
  if (!item || !knowledgeBaseId) {
    return undefined
  }
  const origin: QuestionOrigin = { knowledge_base_id: knowledgeBaseId }
  const knowledgeId = item.knowledge_id?.trim()
  if (knowledgeId) {
    origin.knowledge_id = knowledgeId
  }
  return origin
}
