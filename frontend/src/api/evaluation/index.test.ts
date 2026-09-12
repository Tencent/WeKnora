import assert from 'node:assert/strict'
import test from 'node:test'

import { buildEvaluationTaskQuery, normalizeEvaluationComparisonSelection } from './query'

test('evaluation task query repeats labels and preserves all active filters', () => {
  const query = buildEvaluationTaskQuery({
    status: 2,
    datasetId: 'dataset-a',
    datasetVersionId: 'version-a',
    modelId: 'chat-a',
    startedFrom: '2026-08-31T00:00:00Z',
    startedTo: '2026-08-31T12:00:00Z',
    labels: ['baseline', 'retrieval'],
    pageSize: 100,
    cursor: 'opaque',
  })

  assert.deepEqual(query.getAll('label'), ['baseline', 'retrieval'])
  assert.equal(query.get('status'), '2')
  assert.equal(query.get('dataset_version_id'), 'version-a')
  assert.equal(query.get('page_size'), '100')
  assert.equal(query.get('cursor'), 'opaque')
})

test('comparison selection is distinct, stable, and bounded to ten tasks', () => {
  assert.deepEqual(
    normalizeEvaluationComparisonSelection(['task-b', 'task-a', 'task-b']),
    ['task-b', 'task-a'],
  )
  assert.throws(
    () => normalizeEvaluationComparisonSelection(Array.from({ length: 11 }, (_, index) => `task-${index}`)),
    /at most 10/i,
  )
})
