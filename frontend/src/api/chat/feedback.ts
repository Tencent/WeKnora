import { put } from '@/utils/request'

// Rating values accepted by the message feedback endpoint.
// 'none' cancels an existing rating.
export type FeedbackRating = 'like' | 'dislike' | 'none'

// Preset dislike reason codes; labels are rendered via i18n
// (chat.feedback.reason*). Must stay in sync with the backend whitelist.
export const FEEDBACK_DISLIKE_REASONS = [
  'inaccurate',
  'incomplete',
  'irrelevant',
  'outdated',
  'other',
] as const

export interface MessageFeedbackPayload {
  rating: FeedbackRating
  reasons?: string[]
  comment?: string
}

export function submitMessageFeedback(
  sessionId: string,
  messageId: string,
  data: MessageFeedbackPayload,
) {
  return put(`/api/v1/messages/${sessionId}/${messageId}/feedback`, data)
}
