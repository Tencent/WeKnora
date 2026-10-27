import test from 'node:test'
import assert from 'node:assert/strict'

import {
  parseStatusToTimelineStatus,
  resolveTimelineHeaderStatus,
  shouldShowProcessingError,
} from './knowledgeProcessingStatus.ts'

test('failed attempts expose their cause while awaiting an automatic retry', () => {
  assert.equal(shouldShowProcessingError(true, 'processing', 'failed'), true)
  assert.equal(shouldShowProcessingError(true, 'failed', 'done'), true)
  assert.equal(shouldShowProcessingError(true, 'processing', 'running'), false)
  assert.equal(shouldShowProcessingError(false, 'failed', 'failed'), false)
})

test('latest attempt uses failed knowledge status even when root span is done', () => {
  assert.equal(resolveTimelineHeaderStatus({
    parseStatus: 'failed',
    traceStatus: 'done',
    isLatestAttempt: true,
  }), 'failed')
})

test('latest attempt keeps finalizing distinct from a completed root span', () => {
  assert.equal(resolveTimelineHeaderStatus({
    parseStatus: 'finalizing',
    traceStatus: 'done',
    isLatestAttempt: true,
  }), 'finalizing')
})

test('historical attempt keeps its own root status', () => {
  assert.equal(resolveTimelineHeaderStatus({
    parseStatus: 'failed',
    traceStatus: 'done',
    isLatestAttempt: false,
  }), 'done')
})

test('parse status maps completed and processing into timeline vocabulary', () => {
  assert.equal(parseStatusToTimelineStatus('completed'), 'done')
  assert.equal(parseStatusToTimelineStatus('processing'), 'running')
})
