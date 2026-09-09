import assert from 'node:assert/strict'
import test from 'node:test'
import { expandSteerForksInHistory, forkAfterInjectedUser } from './steerStreamFork.ts'

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
