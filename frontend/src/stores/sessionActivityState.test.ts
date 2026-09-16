import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive } from 'vue'
import { createSessionActivityState, type SessionActivity } from './sessionActivityState.ts'

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
