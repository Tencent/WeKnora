import assert from 'node:assert/strict'
import test from 'node:test'
import { createCachedResource, createKeyedSnapshotCache } from './resourceCache.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

test('concurrent ensure calls share one request; a later ensure issues a new one', async () => {
  const calls: Array<ReturnType<typeof deferred<number>>> = []
  const applied: number[] = []
  const resource = createCachedResource(
    () => {
      const d = deferred<number>()
      calls.push(d)
      return d.promise
    },
    (v) => applied.push(v),
  )

  const a = resource.ensure()
  const b = resource.ensure()
  assert.equal(calls.length, 1, 'mount burst is deduplicated')
  calls[0].resolve(1)
  await Promise.all([a, b])
  assert.deepEqual(applied, [1])
  assert.equal(resource.isLoaded(), true)

  // No TTL: the next ensure after the first one settled fetches again.
  const c = resource.ensure()
  assert.equal(calls.length, 2)
  calls[1].resolve(2)
  await c
  assert.deepEqual(applied, [1, 2])
})

test('invalidate discards the in-flight response and the next ensure waits for a fresh one', async () => {
  const calls: Array<ReturnType<typeof deferred<number>>> = []
  const applied: number[] = []
  const resource = createCachedResource(
    () => {
      const d = deferred<number>()
      calls.push(d)
      return d.promise
    },
    (v) => applied.push(v),
  )

  const stale = resource.ensure()
  resource.invalidate()
  const fresh = resource.ensure()
  assert.equal(calls.length, 1, 'fresh request is queued behind the in-flight one')
  calls[0].resolve(1)
  await stale
  assert.deepEqual(applied, [], 'stale response is not applied')
  assert.equal(calls.length, 2)
  calls[1].resolve(2)
  await fresh
  assert.deepEqual(applied, [2])
  assert.equal(resource.isLoaded(), true)
})

test('markLoaded keeps an older in-flight response from overwriting the replaced snapshot', async () => {
  const calls: Array<ReturnType<typeof deferred<number>>> = []
  const applied: number[] = []
  const resource = createCachedResource(
    () => {
      const d = deferred<number>()
      calls.push(d)
      return d.promise
    },
    (v) => applied.push(v),
  )

  const inflight = resource.ensure()
  resource.markLoaded()
  calls[0].resolve(1)
  await inflight
  assert.deepEqual(applied, [])
  assert.equal(resource.isLoaded(), true)
})

test('keyed snapshot cache loads each key once until invalidated', async () => {
  let loads = 0
  const cache = createKeyedSnapshotCache(async (key: string) => {
    loads += 1
    return `${key}:${loads}`
  })

  const [a, b] = await Promise.all([cache.ensure('kb1'), cache.ensure('kb1')])
  assert.equal(a, 'kb1:1')
  assert.equal(b, 'kb1:1')
  assert.equal(await cache.ensure('kb1'), 'kb1:1', 'snapshot reused without a request')
  assert.equal(loads, 1)

  cache.invalidate('kb1')
  assert.equal(await cache.ensure('kb1'), 'kb1:2')
  assert.equal(await cache.ensure('kb1', true), 'kb1:3', 'force bypasses the snapshot')
})

test('keyed snapshot: a response that lands after invalidate is not written back', async () => {
  const pending = new Map<string, ReturnType<typeof deferred<string>>>()
  let seq = 0
  const cache = createKeyedSnapshotCache((key: string) => {
    const d = deferred<string>()
    pending.set(`${key}#${++seq}`, d)
    return d.promise
  })

  // Sidebar read in flight; the editor saves, invalidates and forces a fresh read.
  const stale = cache.ensure('kb1')
  cache.invalidate('kb1')
  const fresh = cache.ensure('kb1', true)
  assert.equal(pending.size, 2)

  pending.get('kb1#2')!.resolve('saved')
  assert.equal(await fresh, 'saved')
  pending.get('kb1#1')!.resolve('before-save')
  assert.equal(await stale, 'before-save', 'caller of the stale read still gets its value')

  // The snapshot must be the post-save detail, so a later non-force read is correct.
  assert.equal(await cache.ensure('kb1'), 'saved')
  assert.equal(pending.size, 2, 'served from snapshot')
})

test('keyed snapshot: a force read overlapping an in-flight read wins regardless of order', async () => {
  const pending: Array<ReturnType<typeof deferred<string>>> = []
  const cache = createKeyedSnapshotCache((_key: string) => {
    const d = deferred<string>()
    pending.push(d)
    return d.promise
  })

  const first = cache.ensure('kb1')
  const forced = cache.ensure('kb1', true)
  pending[1].resolve('fresh')
  pending[0].resolve('old')
  await Promise.all([first, forced])
  assert.equal(await cache.ensure('kb1'), 'fresh')

  // invalidate() without a key must also fence every in-flight read.
  const late = cache.ensure('kb2')
  cache.invalidate()
  pending[2].resolve('kb2-stale')
  await late
  const reloaded = cache.ensure('kb2')
  assert.equal(pending.length, 4, 'kb2 is reloaded rather than served from the stale write')
  pending[3].resolve('kb2-fresh')
  assert.equal(await reloaded, 'kb2-fresh')
})
