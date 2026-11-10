import assert from 'node:assert/strict'
import test from 'node:test'
import { buildSessionMarkdown, collectAllSessionMessages } from './sessionMarkdown'

const labels = {
  sessionId: 'Session ID',
  exportedAt: 'Exported at',
  user: 'User',
  assistant: 'Assistant',
  attachments: 'Attachments',
  references: 'References',
}

test('collectAllSessionMessages walks backwards and returns chronological unique messages', async () => {
  const pages: Record<string, any[]> = {
    '': [
      { id: '3', role: 'user', content: 'new question', created_at: '2026-01-03T00:00:00Z' },
      { id: '4', role: 'assistant', content: 'new answer', created_at: '2026-01-04T00:00:00Z' },
    ],
    '2026-01-03T00:00:00Z': [
      { id: '1', role: 'user', content: 'old question', created_at: '2026-01-01T00:00:00Z' },
      { id: '2', role: 'assistant', content: 'old answer', created_at: '2026-01-02T00:00:00Z' },
    ],
    '2026-01-01T00:00:00Z': [],
  }

  const result = await collectAllSessionMessages(async (beforeTime) => pages[beforeTime], 2)
  assert.deepEqual(result.map((message) => message.id), ['1', '2', '3', '4'])
})

test('buildSessionMarkdown exports visible conversation content without internal citation tags', () => {
  const markdown = buildSessionMarkdown({
    sessionId: 'session-1',
    title: 'Example chat',
    exportedAt: '2026-01-05T00:00:00Z',
    labels,
    messages: [
      {
        role: 'user',
        content: 'Read this file',
        attachments: [{ file_name: 'notes.md' }],
      },
      {
        role: 'assistant',
        content: 'Done. <kb doc="notes.md" chunk_id="1" />',
        knowledge_references: [
          { knowledge_title: 'Notes', knowledge_source: 'https://example.com/notes' },
          { knowledge_title: 'Notes', knowledge_source: 'https://example.com/notes' },
        ],
      },
      { role: 'system', content: 'hidden system prompt' },
    ],
  })

  assert.match(markdown, /^# Example chat/)
  assert.match(markdown, /## User\n\nRead this file/)
  assert.match(markdown, /### Attachments\n\n- notes\.md/)
  assert.match(markdown, /## Assistant\n\nDone\./)
  assert.match(markdown, /- \[Notes\]\(https:\/\/example\.com\/notes\)/)
  assert.doesNotMatch(markdown, /<kb/)
  assert.doesNotMatch(markdown, /hidden system prompt/)
  assert.equal(markdown.match(/\[Notes\]/g)?.length, 1)
})


test('export messages sort by instants across fractional timestamps and timezone offsets', async () => {
  const messages = [
    { id: 'late', role: 'assistant', created_at: '2026-01-01T00:00:00.100Z' },
    { id: 'early', role: 'user', created_at: '2026-01-01T00:00:00Z' },
    { id: 'earliest', role: 'user', created_at: '2026-01-01T08:59:59+09:00' },
  ]
  const result = await collectAllSessionMessages(async () => messages)
  assert.deepEqual(result.map(message => message.id), ['earliest', 'early', 'late'])
})

test('equal instants use role ordering even with different timestamp representations', async () => {
  const result = await collectAllSessionMessages(async () => [
    { id: 'answer', role: 'assistant', created_at: '2026-01-01T00:00:00Z' },
    { id: 'question', role: 'user', created_at: '2026-01-01T08:00:00+08:00' },
  ])
  assert.deepEqual(result.map(message => message.id), ['question', 'answer'])
})


test('export ordering preserves RFC3339Nano precision below a millisecond', async () => {
  const result = await collectAllSessionMessages(async () => [
    { id: 'later-user', role: 'user', created_at: '2026-01-01T00:00:00.000000200Z' },
    { id: 'earlier-assistant', role: 'assistant', created_at: '2026-01-01T00:00:00.000000100Z' },
  ])
  assert.deepEqual(result.map(message => message.id), ['earlier-assistant', 'later-user'])
})

test('missing and invalid export timestamps have a deterministic fallback', async () => {
  const result = await collectAllSessionMessages(async () => [
    { id: 'valid', role: 'user', created_at: '2026-01-01T00:00:00Z' },
    { id: 'invalid', role: 'user', created_at: 'not-a-date' },
    { id: 'missing', role: 'assistant' },
  ])
  assert.deepEqual(result.map(message => message.id), ['missing', 'invalid', 'valid'])
})
