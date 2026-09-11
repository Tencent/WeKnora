import assert from 'node:assert/strict'
import test from 'node:test'
import { createSkillResourceCache } from './skillResourceCache.ts'
const deferred = <T>() => { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

test('concurrent sandbox and session lookups never reuse another context', async () => {
  const reads: { config: string; session?: string; result: ReturnType<typeof deferred<string>> }[] = []
  const cache = createSkillResourceCache((config, session) => { const result = deferred<string>(); reads.push({ config, session, result }); return result.promise })
  const old = cache.get('office'); const next = cache.get('empty'); const chat = cache.get('office', 'old-session')
  assert.equal(reads.length, 3)
  reads[1].result.resolve('empty'); reads[2].result.resolve('session-image'); reads[0].result.resolve('office')
  assert.deepEqual(await Promise.all([old, next, chat]), ['office', 'empty', 'session-image'])
  assert.equal(await cache.get('office'), 'office')
  assert.equal(reads.length, 3)
})
test('invalidation prevents an old response from repopulating the cache', async () => {
  const old = deferred<string>(); let calls = 0
  const cache = createSkillResourceCache(() => ++calls === 1 ? old.promise : Promise.resolve('new'))
  const pending = cache.get('office'); cache.clear(); old.resolve('old'); await pending
  assert.equal(await cache.get('office'), 'new')
})
test('failed reads can retry, and force during a pending read performs a newer lookup', async () => {
  const old = deferred<string>(); let calls = 0
  const cache = createSkillResourceCache(() => ++calls === 1 ? old.promise : Promise.resolve('new'))
  const initial = cache.get('office'); const refresh = cache.get('office', '', true)
  old.reject(new Error('offline')); await assert.rejects(initial)
  assert.equal(await refresh, 'new'); assert.equal(calls, 2)
})
