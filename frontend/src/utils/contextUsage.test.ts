import assert from 'node:assert/strict'
import test from 'node:test'

import {
  CONTEXT_USAGE_CATEGORIES,
  contextUsageBarSegments,
  contextUsagePercent,
  formatContextUsageCount,
  latestContextUsage,
} from './contextUsage.ts'

test('formatContextUsageCount uses one decimal K like the usage popup', () => {
  assert.equal(formatContextUsageCount(213200), '213.2K')
  assert.equal(formatContextUsageCount(1_000_000), '1000.0K')
  assert.equal(formatContextUsageCount(200000), '200.0K')
  assert.equal(formatContextUsageCount(42), '42')
})

test('contextUsagePercent is used over window', () => {
  assert.equal(contextUsagePercent({ total: 213200, window: 1_000_000 }).toFixed(1), '21.3')
  assert.equal(contextUsagePercent(null), 0)
  assert.equal(contextUsagePercent({ total: 10, window: 0 }), 0)
})

test('contextUsagePercent caps at 100 when usage exceeds the window', () => {
  assert.equal(contextUsagePercent({ total: 150000, window: 100000 }), 100)
  assert.equal(contextUsagePercent({ total: 100000, window: 100000 }), 100)
})

test('latestContextUsage reads the newest assistant snapshot', () => {
  const messages = [
    { role: 'assistant', usage: { context: { total: 10, window: 1000, conversation: 10 } } },
    { role: 'user', content: 'hi' },
    { role: 'assistant', usage: { context: { total: 80, window: 200000, conversation: 50, system_prompt: 20, tools: 10 } } },
  ]
  assert.deepEqual(latestContextUsage(messages), messages[2].usage.context)
  assert.equal(latestContextUsage([]), null)
  assert.equal(latestContextUsage([{ role: 'user' }]), null)
})

const agentSnapshot = { total: 80, window: 200000, conversation: 50, system_prompt: 20, tools: 10 }

test('a completed quick-answer turn does not keep the previous agent snapshot', () => {
  assert.equal(latestContextUsage([
    { role: 'assistant', is_completed: true, usage: { context: agentSnapshot } },
    { role: 'user', content: 'now in quick answer' },
    { role: 'assistant', is_completed: true, content: 'plain rag reply' },
  ]), null)
})

test('an in-flight assistant still shows the last completed agent snapshot', () => {
  assert.deepEqual(latestContextUsage([
    { role: 'assistant', is_completed: true, usage: { context: agentSnapshot } },
    { role: 'user', content: 'follow up' },
    { role: 'assistant', is_completed: false, content: '' },
  ]), agentSnapshot)
})

test('category palette covers the five prompt sources', () => {
  assert.deepEqual(CONTEXT_USAGE_CATEGORIES.map(row => row.key), [
    'system_prompt', 'tools', 'conversation', 'mcp', 'skills',
  ])
})

test('usage bar is a slice of the window, not stretched to 100%', () => {
  const segs = contextUsageBarSegments({
    system_prompt: 8000,
    tools: 4000,
    conversation: 5200,
    total: 17200,
    window: 200000,
  })
  const filled = segs.reduce((sum, row) => sum + row.percent, 0)
  assert.equal(filled.toFixed(1), '8.6')
  assert.ok(filled < 100, 'unused window must remain as empty bar track')
  assert.deepEqual(segs.map(row => row.key), ['system_prompt', 'tools', 'conversation'])
})

test('usage bar stays within 100% when categories overflow the window', () => {
  const segs = contextUsageBarSegments({
    system_prompt: 60000,
    tools: 60000,
    conversation: 30000,
    total: 150000,
    window: 100000,
  })
  const filled = segs.reduce((sum, row) => sum + row.percent, 0)
  assert.equal(filled.toFixed(1), '100.0')
  assert.ok(segs.every(row => row.percent > 0))
})
