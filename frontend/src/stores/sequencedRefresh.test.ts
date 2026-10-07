import assert from 'node:assert/strict'
import test from 'node:test'
import { createSequencedRefresh } from './sequencedRefresh.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function setup() {
  const calls: Array<{ d: ReturnType<typeof deferred<string>>; isCurrent: () => boolean }> = []
  const applied: string[] = []
  const sequencer = createSequencedRefresh<boolean>(async (isCurrent) => {
    const d = deferred<string>()
    calls.push({ d, isCurrent })
    const value = await d.promise
    if (!isCurrent()) return false
    applied.push(value)
    return true
  })
  return { calls, applied, sequencer }
}

test('share reuses the in-flight request; refresh waits for a request issued after the call', async () => {
  const { calls, applied, sequencer } = setup()

  const first = sequencer.share()
  const shared = sequencer.share()
  assert.equal(calls.length, 1)

  // A write happened while the first request was in flight: its response is
  // pre-change, so refresh must not settle on it.
  const refreshed = sequencer.refresh()
  const refreshedAgain = sequencer.refresh()
  assert.equal(calls.length, 1, 'the fresh request is queued behind the in-flight one')

  calls[0].d.resolve('before-write')
  assert.deepEqual([await first, await shared], [true, true])
  assert.deepEqual(applied, ['before-write'])
  assert.equal(calls.length, 2, 'queued refresh started after the first settled')

  calls[1].d.resolve('after-write')
  assert.deepEqual([await refreshed, await refreshedAgain], [true, true])
  assert.deepEqual(applied, ['before-write', 'after-write'])
  assert.equal(sequencer.hasInFlightRequest(), false)
})

test('a response that arrives after invalidate (logout) is not applied', async () => {
  const { calls, applied, sequencer } = setup()

  const late = sequencer.share()
  sequencer.invalidate()
  calls[0].d.resolve('stale-session')
  assert.equal(await late, false)
  assert.deepEqual(applied, [])

  // The next read is a fresh request under the new revision.
  const next = sequencer.share()
  assert.equal(calls.length, 2)
  calls[1].d.resolve('new-session')
  assert.equal(await next, true)
  assert.deepEqual(applied, ['new-session'])
})

test('a queued refresh still runs when the in-flight request rejects', async () => {
  const { calls, applied, sequencer } = setup()

  const failing = sequencer.share()
  const refreshed = sequencer.refresh()
  calls[0].d.reject(new Error('network'))
  await assert.rejects(failing)
  assert.equal(calls.length, 2)
  calls[1].d.resolve('recovered')
  assert.equal(await refreshed, true)
  assert.deepEqual(applied, ['recovered'])
})
