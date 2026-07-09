import assert from 'node:assert/strict'
import test from 'node:test'
import {
  autoRefreshDelay,
  formatElapsed,
  formatProgress,
  formatStageCount,
  previewItems,
  sortStages,
  stateTone,
} from '../src/views/processingDashboard/format.ts'

test('sortStages keeps fixed logical order', () => {
  const stages = sortStages([
    { key: 'wiki' },
    { key: 'docreader' },
    { key: 'graph' },
  ])
  assert.deepEqual(stages.map(s => s.key), ['docreader', 'graph', 'wiki'])
})

test('formatProgress requires reliable denominator', () => {
  assert.equal(formatProgress(null, '解析中'), '解析中')
  assert.equal(formatProgress({ completed: 0, total: 0, failed: 0, unit: 'chunk', reliable: true }, '索引构建中'), '索引构建中')
  assert.equal(formatProgress({ completed: 83, total: 120, failed: 0, unit: 'chunk', reliable: true }, '图谱抽取中'), '83/120 Chunk')
  assert.equal(formatProgress({ completed: 8, total: 12, failed: 0, unit: 'batch', reliable: true }, ''), '8/12 批')
})

test('previewItems limits running preview', () => {
  const items = Array.from({ length: 8 }, (_, i) => ({ knowledge_id: String(i) }))
  assert.equal(previewItems(items, 5).length, 5)
})

test('formatElapsed is compact', () => {
  assert.equal(formatElapsed(83000), '1m23s')
  assert.equal(formatElapsed(2 * 3600_000 + 14 * 60_000), '2h14m')
})

test('stateTone maps visible states', () => {
  assert.equal(stateTone('running'), 'primary')
  assert.equal(stateTone('retrying'), 'warning')
  assert.equal(stateTone('failed'), 'danger')
})

test('autoRefreshDelay supports disabled and seconds', () => {
  assert.equal(autoRefreshDelay(0), null)
  assert.equal(autoRefreshDelay(10), 10000)
})

test('formatStageCount marks unreliable partial counts', () => {
  assert.equal(formatStageCount(3, true), '3')
  assert.equal(formatStageCount(3, false), '>=3')
})
