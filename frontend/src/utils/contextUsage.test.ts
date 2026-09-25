import assert from 'node:assert/strict'
import test from 'node:test'

import {
  CONTEXT_USAGE_CATEGORIES,
  contextUsageBarSegments,
  contextUsageFreeSpace,
  contextUsageGroupRows,
  contextUsagePercent,
  contextUsageThresholdPercent,
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
  const latest = messages[2]?.usage?.context
  assert.ok(latest)
  assert.deepEqual(latestContextUsage(messages), latest)
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

test('an in-flight assistant with a live snapshot wins over the previous turn', () => {
  const live = { total: 120, window: 200000, conversation: 90, tools: 20 }
  assert.deepEqual(latestContextUsage([
    { role: 'assistant', is_completed: true, usage: { context: agentSnapshot } },
    { role: 'user', content: 'follow up' },
    { role: 'assistant', is_completed: false, usage: { context: live } },
  ]), live)
})

test('category palette covers the eight prompt sources in display order', () => {
  assert.deepEqual(CONTEXT_USAGE_CATEGORIES.map(row => row.key), [
    'system_prompt', 'memory', 'skills',
    'tools', 'mcp',
    'conversation', 'reasoning',
    'tool_results',
  ])
})

test('group rows roll up their own categories', () => {
  const rows = contextUsageGroupRows({
    system_prompt: 100, memory: 20, skills: 30,
    tools: 400, mcp: 50,
    conversation: 1000, reasoning: 700,
    tool_results: 5000,
    total: 7300, window: 200000,
  })
  assert.deepEqual(rows.map(row => [row.key, row.tokens]), [
    ['instructions', 150],
    ['toolDefs', 450],
    ['dialogue', 1700],
    ['toolOutput', 5000],
  ])
})

test('free space is whatever the window has left', () => {
  assert.equal(contextUsageFreeSpace({ total: 7300, window: 200000 }), 192700)
  assert.equal(contextUsageFreeSpace({ total: 250000, window: 200000 }), 0)
  assert.equal(contextUsageFreeSpace(null), 0)
})

test('threshold tick sits where compaction fires', () => {
  assert.equal(contextUsageThresholdPercent({ window: 200000, threshold: 180000 }), 90)
  assert.equal(contextUsageThresholdPercent({ window: 200000 }), 0)
  assert.equal(contextUsageThresholdPercent(null), 0)
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
