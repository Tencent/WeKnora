import assert from 'node:assert/strict'
import test from 'node:test'
import { createVersionedRequestCoordinator } from './versionedRequest.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('force queues a fresh request behind an existing request', async () => {
  const first = deferred<string>()
  const second = deferred<string>()
  const requests = [first, second]
  const applied: string[] = []
  let calls = 0
  const coordinator = createVersionedRequestCoordinator(
    () => requests[calls++].promise,
    value => applied.push(value)
  )

  const initial = coordinator.fetch()
  const forced = coordinator.fetch(true)
  assert.equal(calls, 1)

  first.resolve('old')
  await initial
  await new Promise<void>(resolve => queueMicrotask(resolve))
  assert.equal(calls, 2)

  second.resolve('fresh')
  await forced
  assert.deepEqual(applied, ['old', 'fresh'])
})

test('an invalidated older response cannot overwrite newer state', async () => {
  const first = deferred<string>()
  const second = deferred<string>()
  const requests = [first, second]
  const applied: string[] = []
  let calls = 0
  const coordinator = createVersionedRequestCoordinator(
    () => requests[calls++].promise,
    value => applied.push(value)
  )

  const initial = coordinator.fetch()
  coordinator.invalidate()
  const forced = coordinator.fetch(true)

  first.resolve('stale')
  await initial
  await new Promise<void>(resolve => queueMicrotask(resolve))
  second.resolve('fresh')
  await forced

  assert.deepEqual(applied, ['fresh'])
})

test('a newer force request extends an active force queue after invalidation', async () => {
  const first = deferred<string>()
  const second = deferred<string>()
  const third = deferred<string>()
  const requests = [first, second, third]
  const applied: string[] = []
  let calls = 0
  const coordinator = createVersionedRequestCoordinator(
    () => requests[calls++].promise,
    value => applied.push(value)
  )

  void coordinator.fetch()
  const firstForce = coordinator.fetch(true)
  first.resolve('first')
  await new Promise<void>(resolve => queueMicrotask(resolve))

  coordinator.invalidate()
  const secondForce = coordinator.fetch(true)
  second.resolve('stale-second')
  await new Promise<void>(resolve => queueMicrotask(resolve))
  assert.equal(calls, 3)

  third.resolve('fresh-third')
  await Promise.all([firstForce, secondForce])
  assert.deepEqual(applied, ['first', 'fresh-third'])
})
