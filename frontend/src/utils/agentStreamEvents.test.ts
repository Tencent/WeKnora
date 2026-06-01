import assert from 'node:assert/strict'
import test from 'node:test'

import { canPromoteTrailingThinkingToAnswer } from './agentStreamEvents.ts'

test('allows natural-stop thinking promotion when no tools were used', () => {
  assert.equal(canPromoteTrailingThinkingToAnswer([
    { type: 'thinking', content: 'Final answer written as plain text.' },
    { type: 'agent_complete' },
  ]), true)
})

test('does not promote thinking after agent tool calls', () => {
  assert.equal(canPromoteTrailingThinkingToAnswer([
    { type: 'thinking', content: 'Need to query wiki first.' },
    { type: 'tool_call', tool_name: 'wiki_search' },
    { type: 'tool_result', tool_name: 'wiki_search' },
    { type: 'agent_complete' },
  ]), false)
})
