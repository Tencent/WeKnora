import assert from 'node:assert/strict'
import test from 'node:test'
import {
  mergeBackgroundWork,
  createBackgroundWorkPoller,
} from './backgroundWork.ts'
import type { BackgroundWorkItem } from '../api/chat/background-work.ts'
import { get, post } from '../utils/request.ts'

test('merge keeps newest state per work_id, running first', () => {
  const existing: BackgroundWorkItem[] = [
    { work_id: 'sj_a', kind: 'shell', title: 'build', state: 'running' },
    { work_id: 'sj_b', kind: 'shell', title: 'tests', state: 'running' },
  ]
  const incoming: BackgroundWorkItem[] = [
    { work_id: 'sj_b', kind: 'shell', title: 'tests', state: 'succeeded', finished_at: '2026-10-16T00:00:00Z' },
    { work_id: 'sj_c', kind: 'shell', title: 'new', state: 'running' },
  ]
  const merged = mergeBackgroundWork(existing, incoming)
  assert.deepEqual(merged.map(i => i.work_id), ['sj_a', 'sj_c', 'sj_b'])
  assert.equal(merged[2].state, 'succeeded', '新数据覆盖旧状态')
})

test('poller retries after a list error instead of stopping', async () => {
  let calls = 0
  const seen: BackgroundWorkItem[][] = []
  const poller = createBackgroundWorkPoller({
    sessionId: 's1',
    intervalMs: 15,
    list: async () => {
      calls += 1
      if (calls === 1) throw new Error('unavailable')
      return [{ work_id: 'sj_a', kind: 'shell', title: 'build', state: 'running' }]
    },
    onUpdate: (items) => { seen.push(items) },
    onError: () => {},
  })
  poller.start()
  await new Promise((resolve) => setTimeout(resolve, 80))
  poller.stop()
  assert.ok(calls >= 2, '5xx 之后还要再拉')
  assert.ok(seen.some((items) => items.some((item) => item.work_id === 'sj_a')))
})

test('poller stops on empty list and ignores stale session responses', async () => {
  const calls: string[] = []
  let respond: (v: any) => void = () => {}
  const gate = new Promise<void>(resolve => { respond = resolve })
  const fakeGet = (url: string) => {
    calls.push(url)
    return gate.then(() => ({ data: [] }))
  }
  const poller = createBackgroundWorkPoller({
    sessionId: 's1',
    intervalMs: 10,
    list: fakeGet,
    onUpdate: () => {},
  })
  poller.start()
  poller.stop() // 切会话：立即停
  respond(undefined)
  await new Promise(r => setTimeout(r, 30))
  assert.equal(calls.length, 1, '停止后不再发请求')
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

for (const staleItems of [[], [{ work_id: 'sj_old', kind: 'shell', title: 'old', state: 'running' }]] as BackgroundWorkItem[][]) {
  test(`manual refresh cannot replace another session or stop its polling (${staleItems.length} old items)`, async () => {
    let session = 'A'
    const requests: { session: string; response: ReturnType<typeof deferred<BackgroundWorkItem[]>> }[] = []
    const seen: BackgroundWorkItem[][] = []
    const poller = createBackgroundWorkPoller({
      sessionId: () => session,
      intervalMs: 60_000,
      list: (id) => {
        const response = deferred<BackgroundWorkItem[]>()
        requests.push({ session: id, response })
        return response.promise // Deliberately ignore AbortSignal, like a late network response.
      },
      onUpdate: items => seen.push(items),
    })
    try {
      const refreshA = poller.refresh()
      session = 'B'
      poller.stop()
      poller.start()
      const current: BackgroundWorkItem[] = [{ work_id: 'sj_new', kind: 'shell', state: 'running', title: 'new' }]
      requests[1].response.resolve(current)
      await Promise.resolve()
      requests[0].response.resolve(staleItems)
      await refreshA
      assert.deepEqual(seen, [current])
      const count = requests.length
      poller.start()
      assert.equal(requests.length, count, 'stale empty response must not stop B polling')
    } finally { poller.dispose() }
  })
}

test('manual refresh is invalidated on switch away and back, unmount, and newer refresh', async () => {
  const requests: ReturnType<typeof deferred<BackgroundWorkItem[]>>[] = []
  const seen: BackgroundWorkItem[][] = []
  const poller = createBackgroundWorkPoller({
    sessionId: 'A',
    list: () => { const response = deferred<BackgroundWorkItem[]>(); requests.push(response); return response.promise },
    onUpdate: items => seen.push(items),
  })
  const first = poller.refresh()
  poller.stop() // A -> B -> A must invalidate the original A request too.
  const second = poller.refresh()
  const third = poller.refresh()
  requests[1].resolve([{ work_id: 'sj_old', kind: 'shell', state: 'running', title: '' }])
  requests[0].resolve([])
  await Promise.all([first, second])
  assert.deepEqual(seen, [])
  poller.dispose()
  requests[2].resolve([{ work_id: 'sj_late', kind: 'shell', state: 'running', title: '' }])
  await third
  await poller.refresh() // A cancellation completing after unmount must not restart polling.
  poller.start()
  assert.equal(requests.length, 3)
  assert.deepEqual(seen, [])
})
