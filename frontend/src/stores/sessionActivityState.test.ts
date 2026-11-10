import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive } from 'vue'
import {
  createSessionActivityState,
  restoreSessionActivity,
  serializeSessionActivity,
  type SessionActivity,
} from './sessionActivityState.ts'

test('tracks multiple sessions and keeps detached replies until completion', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  let completed = false
  const requested: string[] = []
  const state = createSessionActivityState(entries, async id => {
    requested.push(id)
    return [{ id: 'reply-a', role: 'assistant', is_completed: completed }]
  })
  state.update('a', true, 'reply-a')
  state.update('b', true, 'reply-b')
  state.detach('a')
  await state.refresh()
  assert.deepEqual(requested, ['a'])
  assert.ok(entries.a)
  assert.ok(entries.b)
  completed = true
  await state.refresh()
  assert.equal(entries.a, undefined)
  state.update('b', false)
  assert.deepEqual(Object.keys(entries), [])
})

test('a stale poll cannot clear a reattached stream or a new turn', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  let resolve!: (messages: any[]) => void
  const state = createSessionActivityState(entries, () => new Promise(done => { resolve = done }))
  state.update('a', true, 'old')
  state.detach('a')
  const poll = state.refresh()
  state.update('a', true, 'new')
  resolve([{ id: 'old', role: 'assistant', is_completed: true }])
  await poll
  assert.equal(entries.a?.messageId, 'new')
  assert.equal(entries.a?.detached, false)
})

test('handles navigation before the assistant message arrives and clears deleted sessions', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  let messages: any[] = []
  const state = createSessionActivityState(entries, async () => messages)
  state.update('a', true)
  state.detach('a')
  await state.refresh()
  assert.ok(entries.a)
  messages = [{ id: 'reply', role: 'assistant', is_completed: false }]
  await state.refresh()
  assert.equal(entries.a?.messageId, 'reply')
  messages = []
  await state.refresh()
  assert.equal(entries.a, undefined)
})

test('transient failures preserve the marker and repeated failures clear it', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const state = createSessionActivityState(entries, async () => { throw new Error('offline') })
  state.update('a', true)
  state.detach('a')
  await state.refresh()
  assert.ok(entries.a)
  await state.refresh()
  await state.refresh()
  assert.equal(entries.a, undefined)
})

test('a detached reply that finishes off-screen stays unread until the session is opened', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const unread = reactive<Record<string, true>>({})
  const state = createSessionActivityState(entries, async () => [
    { id: 'reply', role: 'assistant', is_completed: true },
  ], unread)
  state.update('a', true, 'reply')
  state.detach('a')
  state.setViewing('b')
  await state.refresh()
  assert.equal(entries.a, undefined)
  assert.equal(unread.a, true)
  state.setViewing('a')
  assert.equal(unread.a, undefined)
})

test('no unread marker for the session in view, a vanished reply, or a new turn', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const unread = reactive<Record<string, true>>({})
  let messages: any[] = [{ id: 'reply', role: 'assistant', is_completed: true }]
  const state = createSessionActivityState(entries, async () => messages, unread)
  state.update('a', true, 'reply')
  state.detach('a')
  state.setViewing('a')
  await state.refresh()
  assert.equal(unread.a, undefined, 'already looking at it')

  state.setViewing('')
  state.update('b', true, 'gone')
  state.detach('b')
  messages = []
  await state.refresh()
  assert.equal(unread.b, undefined, 'a reply that disappeared is not news')

  unread.c = true
  state.update('c', true, 'next')
  assert.equal(unread.c, undefined, 'a new turn supersedes the old unread')
  unread.d = true
  state.clear()
  assert.deepEqual(Object.keys(unread), [])
})

test('a reload restores unread markers and resumes polling every running turn as detached', async () => {
  const saved = serializeSessionActivity(
    {
      live: { messageId: 'r1', detached: false, failures: 0 },
      away: { messageId: 'r2', detached: true, failures: 2 },
    },
    { done: true },
  )
  const entries = reactive<Record<string, SessionActivity>>({})
  const unread = reactive<Record<string, true>>({})
  restoreSessionActivity(saved, entries, unread)
  assert.deepEqual({ ...entries }, {
    live: { messageId: 'r1', detached: true, failures: 0 },
    away: { messageId: 'r2', detached: true, failures: 0 },
  })
  assert.deepEqual(Object.keys(unread), ['done'])

  const state = createSessionActivityState(entries, async id => [
    { id: id === 'live' ? 'r1' : 'r2', role: 'assistant', is_completed: true },
  ], unread)
  await state.refresh()
  assert.deepEqual(Object.keys(unread).sort(), ['away', 'done', 'live'])
})

test('restoring ignores corrupt storage and never overrides a live entry', () => {
  const entries = reactive<Record<string, SessionActivity>>({
    a: { messageId: 'new', detached: false, failures: 0 },
  })
  const unread = reactive<Record<string, true>>({})
  restoreSessionActivity('{not json', entries, unread)
  restoreSessionActivity(null, entries, unread)
  restoreSessionActivity(JSON.stringify({ running: { a: 'old' }, unread: [1, '', 'b'] }), entries, unread)
  assert.equal(entries.a.messageId, 'new')
  assert.equal(entries.a.detached, false)
  assert.deepEqual(Object.keys(unread), ['b'])
})

test('dismissing a removed session drops both markers', () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const unread = reactive<Record<string, true>>({ a: true })
  const state = createSessionActivityState(entries, async () => [], unread)
  state.update('a', true, 'r')
  unread.a = true
  state.dismiss('a')
  assert.equal(entries.a, undefined)
  assert.equal(unread.a, undefined)
})

test('clearing activity while a poll is in flight cannot restore old account state', async () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  let resolve!: (messages: any[]) => void
  const state = createSessionActivityState(entries, () => new Promise(done => { resolve = done }))
  state.update('a', true, 'reply')
  state.detach('a')
  const poll = state.refresh()
  state.clear()
  resolve([{ id: 'reply', role: 'assistant', is_completed: false }])
  await poll
  assert.deepEqual(Object.keys(entries), [])
})
