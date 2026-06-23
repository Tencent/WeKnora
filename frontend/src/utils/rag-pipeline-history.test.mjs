import assert from 'node:assert/strict'
import test from 'node:test'
import {
  ensureRagPipelineHistoryStream,
  hasRagPipelineToolEvents,
  synthesizeRagPipelineToolEvents,
} from './rag-pipeline-history.ts'

test('synthesizeRagPipelineToolEvents builds completed retrieval steps', () => {
  const events = synthesizeRagPipelineToolEvents({
    knowledge_references: [
      { knowledge_id: 'a' },
      { knowledge_id: 'a' },
      { knowledge_id: 'b' },
    ],
  })

  assert.equal(events.length, 2)
  assert.equal(events[0].tool_name, 'query_understand')
  assert.equal(events[1].tool_name, 'knowledge_search')
  assert.equal(events[1].tool_data.count, 3)
})

test('ensureRagPipelineHistoryStream restores quick-answer history after reload', () => {
  const item = {
    is_completed: true,
    content: 'final answer',
    knowledge_references: [{ knowledge_id: 'doc-1' }],
    agentEventStream: [],
  }

  ensureRagPipelineHistoryStream(item)

  assert.equal(item.isAgentMode, true)
  assert.equal(item.hideContent, true)
  assert.equal(hasRagPipelineToolEvents(item.agentEventStream), true)
  assert.equal(
    item.agentEventStream.some((event) => event.type === 'answer'),
    true,
  )
})

test('ensureRagPipelineHistoryStream keeps existing pipeline events', () => {
  const existing = {
    type: 'tool_call',
    tool_name: 'knowledge_search',
    tool_call_id: 'live-1',
    pending: false,
  }
  const item = {
    is_completed: true,
    content: 'answer',
    agentEventStream: [existing],
  }

  ensureRagPipelineHistoryStream(item)

  assert.equal(item.agentEventStream.length, 1)
  assert.equal(item.agentEventStream[0].tool_call_id, 'live-1')
})
