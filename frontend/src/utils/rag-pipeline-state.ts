export interface RagModelAnswerWaitState {
  isCompleted: boolean
  hasAnswer: boolean
  hasThinkingEvent: boolean
  stepCount: number
  allStepsDone: boolean
  hasCompletedRetrievalStep: boolean
}

/**
 * Show a model-answer wait state only in the quiet gap after every visible RAG
 * pipeline step has finished and before the model emits thinking or answer text.
 */
export function shouldShowRagModelAnswerWait(state: RagModelAnswerWaitState): boolean {
  return (
    !state.isCompleted &&
    !state.hasAnswer &&
    !state.hasThinkingEvent &&
    state.stepCount > 0 &&
    state.allStepsDone &&
    state.hasCompletedRetrievalStep
  )
}
