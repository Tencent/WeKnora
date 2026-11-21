export type FeedbackRating = 'like' | 'dislike' | 'none'

export type FeedbackIntent =
  | { type: 'submit'; rating: 'like' | 'none' }
  | { type: 'prompt' }

export function normalizeFeedbackRating(value: unknown): FeedbackRating {
  return value === 'like' || value === 'dislike' ? value : 'none'
}

// Clicking the active rating cancels it. Dislike on any other state asks for
// an optional reason before it is stored.
export function feedbackIntent(current: FeedbackRating, action: 'like' | 'dislike'): FeedbackIntent {
  if (action === 'like') {
    return { type: 'submit', rating: current === 'like' ? 'none' : 'like' }
  }
  if (current === 'dislike') {
    return { type: 'submit', rating: 'none' }
  }
  return { type: 'prompt' }
}
