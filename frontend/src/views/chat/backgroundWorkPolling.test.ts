import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { createBackgroundWorkPoller } from '../../utils/backgroundWork.ts'

test('chat page retries its first failed background query and stops after a confirmed empty list', async (t) => {
  const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
  const start = source.indexOf('const backgroundPoller = createBackgroundWorkPoller(')
  const end = source.indexOf('\nfunction startBackgroundWork()', start)
  assert.ok(start >= 0 && end > start)
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let calls = 0
  const backgroundItems = { value: [] as any[] }
  const running = [{ work_id: 'sj_0123', kind: 'shell', title: 'build', state: 'running' }]
  const poller = runInNewContext(`${source.slice(start, end)}\nbackgroundPoller`, {
    session_id: { value: 's' }, backgroundItems,
    createBackgroundWorkPoller: (options: Parameters<typeof createBackgroundWorkPoller>[0]) =>
      createBackgroundWorkPoller({ ...options, intervalMs: 10 }),
    listBackgroundWork: async () => {
      calls++
      if (calls === 1) throw new Error('HTTP 503')
      return calls === 2 ? running : []
    },
    replaceBackgroundItems: (items: any[]) => { backgroundItems.value = items },
  })
  try {
    poller.start()
    await Promise.resolve()
    t.mock.timers.tick(10)
    await Promise.resolve()
    assert.equal(calls, 2, 'the real page callback must not stop retries while its initial list is empty')
    assert.deepEqual(backgroundItems.value, running)
    t.mock.timers.tick(10)
    await Promise.resolve()
    assert.deepEqual(backgroundItems.value, [])
    t.mock.timers.tick(100)
    assert.equal(calls, 3, 'a successful empty response ends polling')
  } finally { poller.dispose() }
})
