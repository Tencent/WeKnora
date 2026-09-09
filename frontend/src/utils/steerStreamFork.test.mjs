import assert from 'node:assert/strict'
import test from 'node:test'
import { expandSteerForksInHistory, forkAfterInjectedUser, resetSteerTurnForReplay, steerStepEvents, isAssistantTurnComplete, previewSteerMessage, discardSteerPreview } from './steerStreamFork.ts'

test('inject forks later events onto a new assistant below the user bubble', () => {
  const assistant = {
    id: 'assist-1',
    request_id: 'req-1',
    role: 'assistant',
    is_completed: false,
    isAgentMode: true,
    agentEventStream: [{ type: 'thinking', event_id: 't1', thinking: true, done: false }],
  }
  const queued = {
    id: 'user-2',
    role: 'user',
    content: 'wait, search the other doc',
    request_id: 'req-1',
    steer_id: 'steer-1',
  }
  const list = [
    { id: 'user-1', role: 'user', content: 'original', request_id: 'req-1' },
    assistant,
    queued,
  ]

  const continuation = forkAfterInjectedUser(list, assistant, queued, 'steer-1')

  assert.equal(list.length, 4)
  assert.equal(list[1], assistant)
  assert.equal(list[2], queued)
  assert.equal(list[3], continuation)
  assert.equal(assistant.is_completed, true)
  assert.equal(assistant.steerForked, true)
  assert.equal(assistant.agentEventStream[0].thinking, false)
  assert.equal(continuation.role, 'assistant')
  assert.equal(continuation.is_completed, false)
  assert.equal(continuation.request_id, 'req-1')
  assert.equal(continuation.assistant_message_id, 'assist-1')
  assert.notEqual(continuation.id, assistant.id)
})

test('inject moves a queued user that is not already under the source assistant', () => {
  const assistant = {
    id: 'assist-1',
    request_id: 'req-1',
    role: 'assistant',
    is_completed: false,
    agentEventStream: [],
  }
  const queued = { id: 'user-2', role: 'user', content: 'nudge', steer_id: 's2' }
  const list = [queued, assistant]

  forkAfterInjectedUser(list, assistant, queued, 's2')

  assert.equal(list[0], assistant)
  assert.equal(list[1], queued)
  assert.equal(list[2].role, 'assistant')
})

// continue-stream replays the whole event log, so after a refresh the
// injection arrives again for a transcript history has already split. Redoing
// the fork would duplicate the user bubble and seal the segment that is still
// streaming.
test('replaying an injection onto an already split turn is a no-op', () => {
  const sealed = {
    id: 'a0',
    role: 'assistant',
    request_id: 'req-1',
    is_completed: true,
    steerForked: true,
    agentEventStream: [{ type: 'thinking', content: 'before' }],
  }
  const injected = { id: 'u1', role: 'user', request_id: 'req-1', content: 'also check B' }
  const live = {
    id: 'a0:steer:1',
    assistant_message_id: 'a0',
    role: 'assistant',
    request_id: 'req-1',
    is_completed: false,
    agentEventStream: [],
  }
  const list = [{ id: 'u0', role: 'user', content: 'start' }, sealed, injected, live]

  const continuation = forkAfterInjectedUser(list, live, injected, 'steer-1')

  assert.equal(continuation, live, 'must reuse the live segment as the continuation')
  assert.equal(list.length, 4, 'no extra bubble or segment may be inserted')
  assert.equal(list.indexOf(injected), 2, 'the user bubble must not be moved')
  assert.equal(live.is_completed, false, 'the live segment must not be sealed by a replay')
})

// Refreshing while the agent is still working is the case that matters most:
// the turn absorbed an injected message, so it gets split, but the trailing
// segment is still the live one. Marking it completed makes the chat view skip
// continue-stream, and the running agent's output never comes back.
test('an in-flight turn stays in-flight after being split', () => {
  const messages = [
    { id: 'u0', role: 'user', request_id: 'req-1', content: 'start' },
    {
      id: 'a0',
      role: 'assistant',
      request_id: 'req-1',
      content: '',
      is_completed: false,
      isAgentMode: true,
      agentEventStream: [{ type: 'thinking', timestamp: 1000, content: 'before' }],
    },
    {
      id: 'u1',
      role: 'user',
      request_id: 'req-1',
      content: 'also check B',
      created_at: '1970-01-01T00:00:02.000Z',
    },
  ]

  const expanded = expandSteerForksInHistory(messages)
  const tail = expanded[expanded.length - 1]

  assert.equal(tail.role, 'assistant')
  assert.equal(tail.is_completed, false, 'the live segment must not be marked completed')
  assert.ok(!tail.steerForked, 'the live segment is not a sealed fork prefix')
  assert.equal(tail.assistant_message_id, 'a0', 'must still address the persisted row')

  // The sealed prefix keeps its own flags.
  assert.equal(expanded[1].is_completed, true)
  assert.equal(expanded[1].steerForked, true)
})

// The completed turn's trailing segment carries the answer, so it must not
// inherit the sealed-prefix flags either — steerForked there suppresses the
// answer toolbar and follow-up suggestions.
test('the trailing segment does not inherit sealed-prefix flags', () => {
  const messages = [
    { id: 'u0', role: 'user', request_id: 'req-1', content: 'start' },
    {
      id: 'a0',
      role: 'assistant',
      request_id: 'req-1',
      content: 'final',
      is_completed: true,
      agentEventStream: [
        { type: 'thinking', timestamp: 1000, content: 'before' },
        { type: 'answer', content: 'final', done: true },
      ],
    },
    { id: 'u1', role: 'user', request_id: 'req-1', content: 'more', created_at: '1970-01-01T00:00:02.000Z' },
  ]

  const tail = expandSteerForksInHistory(messages).at(-1)
  assert.equal(tail.content, 'final')
  assert.equal(tail.is_completed, true)
  assert.ok(!tail.steerForked)
})

test('history reload splits one assistant around later same-request user rows', () => {
  const messages = [
    { id: 'u0', role: 'user', request_id: 'req-1', content: 'start' },
    {
      id: 'a0',
      role: 'assistant',
      request_id: 'req-1',
      content: 'final',
      is_completed: true,
      agentEventStream: [
        { type: 'thinking', timestamp: 1000, content: 'before' },
        { type: 'tool_call', timestamp: 1100, tool_name: 'knowledge_search' },
        { type: 'thinking', timestamp: 3000, content: 'after inject' },
        { type: 'answer', content: 'final', done: true },
      ],
    },
    { id: 'u1', role: 'user', request_id: 'req-1', content: 'also check B', created_at: '1970-01-01T00:00:02.000Z' },
  ]

  const expanded = expandSteerForksInHistory(messages)
  assert.equal(expanded.length, 4)
  assert.equal(expanded[0].id, 'u0')
  assert.equal(expanded[1].id, 'a0')
  assert.equal(expanded[1].steerForked, true)
  assert.equal(expanded[1].content, '')
  assert.deepEqual(
    expanded[1].agentEventStream.map((e) => e.type),
    ['thinking', 'tool_call'],
  )
  assert.equal(expanded[2].id, 'u1')
  assert.equal(expanded[3].role, 'assistant')
  assert.equal(expanded[3].assistant_message_id, 'a0')
  assert.equal(expanded[3].is_completed, true)
  assert.equal(expanded[3].content, 'final')
  assert.deepEqual(
    expanded[3].agentEventStream.map((e) => e.type),
    ['thinking', 'answer'],
  )
})

test('expanding an already split transcript is a no-op', () => {
  const messages = [
    { id: 'u0', role: 'user', request_id: 'req-1', content: 'start' },
    {
      id: 'a0',
      role: 'assistant',
      request_id: 'req-1',
      content: 'final',
      is_completed: true,
      agentEventStream: [
        { type: 'thinking', timestamp: 1000, content: 'before' },
        { type: 'thinking', timestamp: 3000, content: 'after inject' },
        { type: 'answer', content: 'final', done: true },
      ],
    },
    { id: 'u1', role: 'user', request_id: 'req-1', content: 'also check B', created_at: '1970-01-01T00:00:02.000Z' },
  ]

  const once = expandSteerForksInHistory(messages)
  const twice = expandSteerForksInHistory(once)
  assert.equal(twice.length, once.length)
  assert.equal(twice[1].steerForked, true)
  assert.equal(twice[3].assistant_message_id, 'a0')
  assert.equal(twice[3].id, once[3].id)
})

test('explicit boundaries preserve drafts, repeated inputs and delivery order despite equal timestamps', () => {
  const users = ['u2', 'u1'].map(id => ({ id, role: 'user', request_id: 'r', content: 'revise', created_at: '1970-01-01T00:00:00Z' }))
  const assistant = { id: 'a', role: 'assistant', request_id: 'r', content: 'final', is_completed: true,
    used_memories: [{ id: 'memory' }], agentEventStream: [
      { type: 'answer', content: 'first draft', done: true, intermediate_answer: true },
      { type: 'user_message_injected', user_message_id: 'u1' },
      { type: 'answer', content: 'second draft', done: true, intermediate_answer: true },
      { type: 'user_message_injected', user_message_id: 'u2' },
      { type: 'answer', content: 'final', done: true },
    ] }
  const out = expandSteerForksInHistory([assistant, ...users])
  assert.deepEqual(out.map(m => m.content), ['first draft', 'revise', 'second draft', 'revise', 'final'])
  assert.equal(out[1].id, 'u1')
  assert.equal(out[3].id, 'u2')
  assert.equal(out[1].isSteer, true)
  assert.equal(out.filter(m => m.used_memories?.length).length, 1)
})

test('sealing a draft finishes its text stream without discarding the draft', () => {
  const source = { id: 'a', role: 'assistant', request_id: 'r', content: 'draft', agentEventStream: [{ type: 'answer', content: 'draft', done: false }] }
  const list = [source]
  forkAfterInjectedUser(list, source, { id: 'u1', role: 'user', request_id: 'r', content: 'revise' }, 's1')
  assert.equal(source.content, 'draft')
  assert.equal(source.agentEventStream[0].done, true)
})

test('replay walks successive existing segments rather than duplicating earlier events on the tail', () => {
  const a = { id: 'a', role: 'assistant', request_id: 'r', steerForked: true, content: 'draft', agentEventStream: [{ type: 'answer', content: 'draft' }] }
  const u1 = { id: 'u1', role: 'user', request_id: 'r', content: 'one' }
  const middle = { id: 'a:1', role: 'assistant', request_id: 'r', steerForked: true, agentEventStream: [] }
  const u2 = { id: 'u2', role: 'user', request_id: 'r', content: 'two' }
  const tail = { id: 'a:2', role: 'assistant', request_id: 'r', is_completed: false, agentEventStream: [] }
  const list = [a, u1, middle, u2, tail]
  let cursor = resetSteerTurnForReplay(list, 'r')
  assert.equal(cursor, a)
  cursor.agentEventStream.push({ type: 'answer', content: 'draft' })
  cursor = forkAfterInjectedUser(list, cursor, u1, 's1')
  assert.equal(cursor, middle)
  cursor.agentEventStream.push({ type: 'thinking', content: 'work' })
  cursor = forkAfterInjectedUser(list, cursor, u2, 's2')
  assert.equal(cursor, tail)
  assert.equal(list.length, 5)
  assert.equal(a.agentEventStream.length, 1)
  assert.equal(middle.agentEventStream.length, 1)
  assert.equal(tail.agentEventStream.length, 0)
  assert.equal(a.is_completed, true)
  assert.equal(middle.is_completed, true)
  assert.equal(tail.is_completed, false)
})

test('step boundary events retain the server delivery order', () => {
  assert.deepEqual(steerStepEvents({ user_messages_before: ['u2', 'u1'] }).map(e => e.user_message_id), ['u2', 'u1'])
})

test('only run completion ends the task; draft completion and sealed prefixes do not', () => {
  assert.equal(isAssistantTurnComplete({ is_completed: false, agentEventStream: [{ type: 'answer', done: true }] }), false)
  assert.equal(isAssistantTurnComplete({ is_completed: true, steerForked: true }), false)
  assert.equal(isAssistantTurnComplete({ agentEventStream: [{ type: 'agent_complete' }] }), true)
  assert.equal(isAssistantTurnComplete({ agentEventStream: [{ type: 'stop' }] }), true)
  assert.equal(isAssistantTurnComplete({ is_completed: true }), true)
})


test('inject appears immediately and the delivery receipt reuses its bubble', () => {
  const assistant = { id: 'a', role: 'assistant', request_id: 'r', is_completed: false, agentEventStream: [{ type: 'thinking', done: false }] }
  const list = [assistant]
  const preview = previewSteerMessage(list, { steer_id: 's', content: '补充' })
  assert.equal(list[1], preview)
  assert.equal(assistant.is_completed, false)
  assert.equal(assistant.agentEventStream[0].done, false)
  assert.equal(previewSteerMessage(list, { steer_id: 's', content: '补充' }), preview)
  assert.equal(list.length, 2)
  forkAfterInjectedUser(list, assistant, preview, 's')
  assert.equal(list.length, 3)
  assert.equal(list[1], preview)
  assert.equal(preview._steerPending, undefined)
  discardSteerPreview(list, 's')
  assert.equal(list.length, 3, 'removal must never discard a consumed message')
})

test('failed promotion can restore its queue without leaving a duplicate bubble', () => {
  const list = []
  previewSteerMessage(list, { steer_id: 's', content: '补充' })
  discardSteerPreview(list, 's')
  assert.equal(list.length, 0)
  previewSteerMessage(list, { steer_id: 's', content: '补充' })
  assert.equal(list.length, 1)
})
