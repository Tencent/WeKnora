import assert from 'node:assert/strict'
import test from 'node:test'
import { shouldShowRagModelAnswerWait } from './rag-pipeline-state.ts'

const completedRetrieval = {
  isCompleted: false,
  hasAnswer: false,
  hasThinkingEvent: false,
  stepCount: 2,
  allStepsDone: true,
  hasCompletedRetrievalStep: true,
}

test('shows the model-answer wait state after the RAG pipeline finishes', () => {
  assert.equal(shouldShowRagModelAnswerWait(completedRetrieval), true)
})

test('does not show the model-answer wait state while a pipeline step is pending', () => {
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    allStepsDone: false,
  }), false)
})

test('does not show the model-answer wait state before pipeline events arrive', () => {
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    stepCount: 0,
  }), false)
})

test('does not mistake a completed pre-retrieval step for the model-answer wait', () => {
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    hasCompletedRetrievalStep: false,
  }), false)
})

test('hides the model-answer wait state when thinking, answer, or completion arrives', () => {
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    hasThinkingEvent: true,
  }), false)
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    hasAnswer: true,
  }), false)
  assert.equal(shouldShowRagModelAnswerWait({
    ...completedRetrieval,
    isCompleted: true,
  }), false)
})
