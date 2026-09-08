import assert from 'node:assert/strict'
import test from 'node:test'
import {
  collectSessionArtifacts,
  formatArtifactDateTime,
  formatArtifactSize,
} from './sessionArtifacts.ts'

test('collectSessionArtifacts keeps per-message download indexes', () => {
  const items = collectSessionArtifacts([
    { role: 'user', id: 'u1', content: 'hi' },
    {
      role: 'assistant',
      id: 'a1',
      artifacts: [{ file_name: 'a.csv', file_size: 12 }],
    },
    {
      role: 'assistant',
      id: 'a2',
      artifacts: [
        { index: 0, file_name: 'chart.html' },
        { index: 1, file_name: 'plot.png' },
      ],
    },
  ])
  assert.deepEqual(
    items.map((item) => ({ messageId: item.messageId, index: item.index, file_name: item.file_name })),
    [
      { messageId: 'a1', index: 0, file_name: 'a.csv' },
      { messageId: 'a2', index: 0, file_name: 'chart.html' },
      { messageId: 'a2', index: 1, file_name: 'plot.png' },
    ],
  )
})

test('collectSessionArtifacts falls back to request_id before the row is persisted', () => {
  const items = collectSessionArtifacts([
    { request_id: 'req-9', artifacts: [{ file_name: 'out.md' }] },
  ])
  assert.equal(items[0]?.messageId, 'req-9')
  assert.equal(items[0]?.index, 0)
})

test('collectSessionArtifacts skips empty or unidentifiable rows', () => {
  assert.deepEqual(
    collectSessionArtifacts([
      { id: 'a1', artifacts: [] },
      { artifacts: [{ file_name: 'ghost.txt' }] },
      null,
      'nope',
    ]),
    [],
  )
})

test('formatArtifactSize matches the former drawer copy', () => {
  assert.equal(formatArtifactSize(0), '0 B')
  assert.equal(formatArtifactSize(512), '512 B')
  assert.equal(formatArtifactSize(2048), '2.0 KB')
})

test('formatArtifactDateTime renders a stable local timestamp', () => {
  assert.equal(formatArtifactDateTime(''), '—')
  assert.equal(formatArtifactDateTime('not-a-date'), 'not-a-date')
  assert.match(formatArtifactDateTime('2026-09-08T04:05:00Z'), /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
})
