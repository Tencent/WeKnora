import assert from 'node:assert/strict'
import { test } from 'node:test'
import { foldProgressIntoThinking } from './agentTimelinePresentation'
import { withoutCollaborationWaits } from './collaborationDisplay'

test('live and recovered progress label the preceding reasoning without changing stored events', () => {
  for (const done of [false, true]) {
    const thought = Object.freeze({ type: 'thinking', event_id: 'r1', content: 'Check the agents', done })
    for (const update of [
      { type: 'progress', event_id: 'p1', content: '我先查看智能体。', iteration: 1 },
      { type: 'answer', event_id: 'p1', content: '我先查看智能体。', superseded: true },
    ]) {
      const input = Object.freeze([thought, Object.freeze(update)])
      const output = foldProgressIntoThinking([...input])
      assert.deepEqual(output, [{ ...thought, title: update.content }])
      assert.equal(output[0].event_id, 'r1') // Preserve expansion identity.
      assert.equal('title' in thought, false)
      assert.equal(input[1].type, update.type)
    }
  }
})

test('tools, approvals and user messages prevent cross-round association even when hidden', () => {
  for (const boundary of [
    { type: 'tool_call', tool_name: 'wait_agent' },
    { type: 'tool_call', tool_name: 'spawn_agent' },
    { type: 'tool_approval_required' },
    { type: 'user_message_injected' },
  ]) {
    const events = [{ type: 'thinking', content: 'Earlier reasoning' }, boundary,
      { type: 'progress', content: 'New update' }]
    const projected = withoutCollaborationWaits(foldProgressIntoThinking(events))
    assert.equal(projected[0].title, undefined)
    assert.equal(projected.at(-1)?.type, 'progress')
  }
})

test('final answers, standalone progress, different rounds and subsequent updates remain separate', () => {
  const events = [
    { type: 'progress', content: 'Starting' },
    { type: 'thinking', content: 'Reasoning', iteration: 1 },
    { type: 'progress', content: 'Different round', iteration: 2 },
    { type: 'thinking', content: 'More reasoning' },
    { type: 'progress', content: 'First update' },
    { type: 'progress', content: 'Second update' },
    { type: 'answer', content: 'Complete deliverable' },
  ]
  const output = foldProgressIntoThinking(events)
  assert.equal(output.length, 6)
  assert.equal(output[1].title, undefined)
  assert.equal(output[3].title, 'First update')
  assert.deepEqual(output.at(-1), events.at(-1))
  assert.equal(output[4].content, 'Second update')
})

test('adjacent visible progress keeps only the latest summary and preserves earlier updates for expansion', async () => {
  const { groupProgressUpdates } = await import('./agentTimelinePresentation')
  const { collaborationTimeline } = await import('./collaborationDisplay')
  const first = Object.freeze({ type: 'progress', event_id: 'p1', content: '等待审批', timestamp: 1 })
  const second = Object.freeze({ type: 'progress', event_id: 'p2', content: '正在读取全文', timestamp: 2 })
  const third = Object.freeze({ type: 'progress', event_id: 'p3', content: '正在整理结果', timestamp: 3 })
  const events = [first, { type: 'tool_call', tool_name: 'wait_agent', success: true }, second, third]
  const projected = groupProgressUpdates(collaborationTimeline(events, undefined))
  assert.equal(projected.length, 1)
  assert.equal(projected[0].event_id, 'p1')
  assert.equal(projected[0].content, third.content)
  assert.deepEqual(projected[0].progress_history, [first.content, second.content])
  assert.equal(first.content, '等待审批')
  assert.equal('progress_history' in third, false)
  const appended = groupProgressUpdates([...projected, { type: 'progress', event_id: 'p4', content: '即将完成' }])
  assert.equal(appended[0].event_id, 'p1')
  assert.deepEqual(appended[0].progress_history, [first.content, second.content, third.content])
})

test('progress grouping preserves approval, reasoning, tool, user input and final answer boundaries', async () => {
  const { groupProgressUpdates } = await import('./agentTimelinePresentation')
  for (const type of ['tool_approval_required', 'agent_prompt_required', 'thinking', 'tool_call', 'user_message_injected', 'answer', 'stop']) {
    const events = [{ type: 'progress', content: 'Before' }, { type, content: 'Boundary' }, { type: 'progress', content: 'After' }]
    assert.deepEqual(groupProgressUpdates(events), events)
  }
})

test('finished untitled reasoning rides on the tool call it led to', async () => {
  const { attachReasoningToTools } = await import('./agentTimelinePresentation')
  const tool = Object.freeze({ type: 'tool_call', tool_call_id: 't1', tool_name: 'knowledge_search' })
  const events = [
    { type: 'thinking', event_id: 'r1', content: 'Search first' }, tool,
    { type: 'thinking', event_id: 'r2', title: '我先读一下文档', content: 'Read it' },
    { type: 'tool_call', tool_call_id: 't2', tool_name: 'read_document' },
    { type: 'thinking', event_id: 'r3', content: 'Still thinking' },
    { type: 'tool_call', tool_call_id: 't3', tool_name: 'web_search' },
    { type: 'thinking', event_id: 'r4', content: 'Wrap up' },
  ]
  const output = attachReasoningToTools(events, (e: any) => e.event_id !== 'r3')
  assert.deepEqual(output.map((e: any) => e.event_id || e.tool_call_id), ['t1', 'r2', 't2', 'r3', 't3', 'r4'])
  assert.deepEqual(output[0].reasoning, { id: 'reasoning-r1', content: 'Search first' })
  assert.equal('reasoning' in tool, false)
  assert.equal(output[2].reasoning, undefined, 'titled narration keeps its own row')
  assert.equal(output[4].reasoning, undefined, 'reasoning that cannot attach keeps its own row')
})

test('earlier steps fold behind recent and attention-demanding rows', async () => {
  const { earlierStepKeys } = await import('./agentTimelinePresentation')
  const rows = (n: number, sticky: string[] = []) => Array.from({ length: n }, (_, i) => ({
    key: `k${i}`, step: true, sticky: sticky.includes(`k${i}`),
  }))
  assert.deepEqual(earlierStepKeys(rows(4)), [], 'folding a single row saves nothing')
  assert.deepEqual(earlierStepKeys(rows(6)), ['k0', 'k1', 'k2'])
  assert.deepEqual(earlierStepKeys(rows(5, ['k1'])), [], 'sticky rows stay out of the fold')
  assert.deepEqual(earlierStepKeys(rows(6, ['k1'])), ['k0', 'k2'])
  assert.deepEqual(earlierStepKeys([...rows(6), { key: 'answer', step: false, sticky: false }]), ['k0', 'k1', 'k2'])
})
