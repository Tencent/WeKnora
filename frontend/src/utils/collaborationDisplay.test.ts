import assert from 'node:assert/strict'
import { test } from 'node:test'
import { collaborationTimeline, collaborationTasks, collaborationWaitState, withoutCollaborationWaits } from './collaborationDisplay'

test('parallel tasks retain separate identities and readable latest messages', () => {
  const tasks = collaborationTasks({ tasks: [
    { id: 'a', name: 'research', status: 'running', latest_message: { content: 'Found evidence' }, progress: { phase: 'tool_running', tool_name: 'web_search' } },
    { member_id: 'b', task_name: 'review', status: 'waiting', latest_message: { arbitrary: 'do not render JSON' } },
  ] })
  assert.deepEqual(tasks.map(t => [t.id, t.name, t.status, t.latestMessage]), [['a','research','running','Found evidence'], ['b','review','waiting','']])
  assert.equal(tasks[0].progress?.tool_name, 'web_search')
})
test('wait polls fold without hiding actual progress or losing active wait', () => {
  const events = [
    { type: 'tool_call', tool_name: 'wait_agent', pending: false, output: '{"timed_out":true}' },
    { type: 'progress', content: 'Review is still running' },
    { type: 'tool_call', tool_name: 'wait_agent', pending: true },
  ]
  assert.equal(collaborationWaitState(events), 'waiting')
  assert.equal(collaborationWaitState(events, true), 'finished')
  assert.deepEqual(withoutCollaborationWaits(events), [events[1]])
  assert.equal(collaborationWaitState([]), undefined)
})
test('cancellation and missing snapshots do not claim task completion', () => {
  assert.deepEqual(collaborationTasks(undefined), [])
  const tasks = collaborationTasks({ tasks: [{ id: 'a', status: 'running', cancel_requested: true }, { id: 'b', status: 'future-status' }] })
  assert.deepEqual(tasks.map(t => t.status), ['cancelling', 'unknown'])
})

test('failed wait remains visible instead of becoming a successful wait summary', () => {
  assert.equal(collaborationWaitState([{ type: 'tool_call', tool_name: 'wait_agent', pending: false, success: false }]), 'failed')
})

test('tool counts include calls represented by the task panel without mutating history', async () => {
  const { countToolCalls } = await import('./collaborationDisplay')
  const events = [
    { type: 'tool_call', tool_name: 'spawn_agent', tool_call_id: 'spawn' },
    { type: 'thinking', content: 'private reasoning' },
    { type: 'progress', content: 'First progress' },
    { type: 'tool_call', tool_name: 'list_agents', tool_call_id: 'list' },
    { type: 'tool_call', tool_name: 'wait_agent', tool_call_id: 'wait-1' },
    { type: 'progress', content: 'Latest progress' },
    { type: 'tool_call', tool_name: 'wait_agent', tool_call_id: 'wait-2' },
    { type: 'tool_approval_required', pending_id: 'one', resolved: false },
    { type: 'agent_prompt_required', pending_id: 'two', resolved: false },
    { type: 'tool_approval_required', pending_id: 'old', resolved: true },
    { type: 'answer', content: 'Result' },
  ]
  assert.equal(countToolCalls(events), 4)
  assert.equal(countToolCalls([...events, events[0]]), 4)
  assert.equal(events.filter(e => e.type === 'progress').length, 2, 'full history is untouched')
})
test('display name is distinct from the logical assignment name', () => {
  const [task] = collaborationTasks({ tasks: [{ id: 'id', name: 'review', display_name: '质量审核员', status: 'succeeded' }] })
  assert.equal(task.displayName, '质量审核员')
  assert.equal(task.name, 'review')
})

test('child rows stay at spawn position and concurrent waits share one row', async () => {
  const { collaborationTimeline } = await import('./collaborationDisplay')
  const events = [
    { type: 'progress', content: 'Starting' },
    { type: 'tool_call', tool_name: 'spawn_agent', arguments: { task_name: 'a' } },
    { type: 'tool_call', tool_name: 'spawn_agent', arguments: { task_name: 'b' } },
    { type: 'tool_call', tool_name: 'wait_agent', pending: false },
    { type: 'progress', content: 'Checking' },
    { type: 'tool_call', tool_name: 'wait_agent', pending: true },
  ]
  const snapshot = { tasks: [{ id: 'a', name: 'a', session_id: 'sa', run_id: 'ra', status: 'running' },
    { id: 'b', name: 'b', session_id: 'sb', run_id: 'rb', status: 'waiting' }] }
  const result = collaborationTimeline(events, snapshot)
  assert.deepEqual(result.map(e => e.type), ['progress', 'child_task', 'child_task', 'progress'])
  assert.equal(result[1].task.sessionId, 'sa')
  assert.equal(result[2].task.sessionId, 'sb')
  assert.equal(result.filter(e => e.type === 'child_wait').length, 0, 'live waiting belongs at the response tail')
  const restored = collaborationTimeline([{ type: 'answer', content: 'Result' }], snapshot, true)
  assert.deepEqual(restored.map(e => e.type), ['child_task', 'child_task', 'answer'])
})


test('live waiting follows approval state and stops on cancellation, completion, or parent resumption', async () => {
  const { collaborationActivity, collaborationTimeline } = await import('./collaborationDisplay')
  const wait = { type: 'tool_call', tool_name: 'wait_agent', pending: true }
  const events: any[] = [{ ...wait, pending: false }, ...Array.from({ length: 60 }, () => ({ type: 'progress', content: 'progress' })), wait]
  const snapshot = { tasks: [{ id: 'a', status: 'running' }, { id: 'b', status: 'running' }] }
  assert.equal(collaborationActivity(events, snapshot), 'waiting')
  const prompt = { type: 'tool_approval_required', resolved: false }
  events.push(prompt)
  assert.equal(collaborationActivity(events, snapshot), 'approval')
  prompt.resolved = true
  assert.equal(collaborationActivity(events, snapshot), 'waiting')
  assert.equal(collaborationActivity(events, snapshot, true), undefined)
  assert.equal(collaborationActivity([...events, { type: 'stop' }], snapshot), undefined)
  assert.equal(collaborationActivity([...events, { type: 'agent_complete' }], snapshot), undefined)
  wait.pending = false
  assert.equal(collaborationActivity(events, snapshot), undefined, 'a running child alone does not mean its parent is waiting')
  const history = collaborationTimeline(events, snapshot, true)
  assert.equal(history.filter(e => e.type === 'child_wait' || e.tool_name === 'wait_agent').length, 0, 'completed polls are not user-facing milestones')
})


test('successful and timed-out waits disappear from history while real errors remain inspectable', async () => {
  const { collaborationTimeline } = await import('./collaborationDisplay')
  const completed = { type: 'tool_call', tool_name: 'wait_agent', pending: false, success: true, tool_data: { timed_out: true } }
  const failure = { ...completed, success: false, output: 'Connection failed' }
  assert.deepEqual(collaborationTimeline([completed], undefined, true), [])
  assert.deepEqual(collaborationTimeline([completed, failure], undefined, true), [failure])
})

test('a continued child stays at its followup position in the new parent turn', () => {
  for (const target of ['child-id', 'novelist', '/root/novelist']) {
    const snapshot = { tasks: [{ id: 'child-id', name: 'novelist', session_id: 'original-session', run_id: 'new-run', status: 'running' }] }
    const events = [
      { type: 'progress', content: '继续写第一章' },
      { type: 'tool_call', tool_name: 'followup_task', arguments: { target }, success: true, timestamp: 'now' },
      { type: 'thinking', content: 'Waiting for the chapter' },
      { type: 'tool_call', tool_name: 'wait_agent', success: true },
    ]
    const rows = collaborationTimeline(events, snapshot)
    assert.deepEqual(rows.map(row => row.type), ['progress', 'child_task', 'thinking'])
    assert.equal(rows[1].task.sessionId, 'original-session')
    assert.equal(rows[1].task.runId, 'new-run')
    assert.equal(rows[1].timestamp, 'now')
    events[1].success = false
    assert.ok(collaborationTimeline(events, snapshot).some(row => row.tool_name === 'followup_task'), 'failed continuation remains visible')
  }
})
