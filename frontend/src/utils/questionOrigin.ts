import type { SuggestedQuestion } from '@/api/agent/index'

// The knowledge source a suggested question was generated from. Sent with the
// picked question so the agent searches that source before answering; the
// backend treats it as a hint inside the turn's scope, never a scope change.
export interface QuestionOrigin {
  knowledge_base_id: string
  knowledge_id?: string
}

export interface PendingQuestionOrigin {
  question: string
  origin: QuestionOrigin
}

// Agent-authored starters have no knowledge source and yield null.
export function questionOriginFromSuggestion(
  item: Pick<SuggestedQuestion, 'question' | 'knowledge_base_id' | 'knowledge_id'> | null | undefined,
): PendingQuestionOrigin | null {
  const knowledgeBaseId = item?.knowledge_base_id?.trim()
  if (!item || !knowledgeBaseId) {
    return null
  }
  const origin: QuestionOrigin = { knowledge_base_id: knowledgeBaseId }
  const knowledgeId = item.knowledge_id?.trim()
  if (knowledgeId) {
    origin.knowledge_id = knowledgeId
  }
  return { question: item.question, origin }
}

// The origin applies only while the text being sent is still the picked
// question; anything else the user sends carries no origin.
export function originForSentText(
  pending: PendingQuestionOrigin | null | undefined,
  text: string,
): QuestionOrigin | undefined {
  if (!pending) {
    return undefined
  }
  return pending.question.trim() === (text || '').trim() ? pending.origin : undefined
}
