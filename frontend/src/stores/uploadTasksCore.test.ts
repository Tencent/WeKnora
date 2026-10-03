import assert from 'node:assert/strict'
import test from 'node:test'
import { ref } from 'vue'
import type { Timers, UploadBatch, UploadPayload } from './uploadQueue.ts'
import { createUploadTasks } from './uploadTasksCore.ts'

type Call = { batch: UploadBatch; payload: UploadPayload; signal: AbortSignal; resolve: (v: unknown) => void; reject: (r: unknown) => void }

const file = (name: string, size = 100) => ({ name, size, webkitRelativePath: '' }) as unknown as File
const flush = () => new Promise(resolve => setImmediate(resolve))
const noTimers: Timers = { setTimer: () => ({}), clearTimer: () => {} }

function setup() {
  const user = ref<{ id: string; nickname?: string } | null>({ id: 'u1' })
  const tenant = ref(1)
  const calls: Call[] = []
  const tasks = createUploadTasks({
    upload: (batch, payload, _progress, signal) => new Promise((resolve, reject) => {
      calls.push({ batch, payload, signal, resolve, reject })
      signal.addEventListener('abort', () => reject({ message: 'Network error' }))
    }),
    queryStatus: async () => [],
    emitListRefresh: () => {},
    owner: [() => user.value?.id, () => tenant.value],
    timers: noTimers,
  })
  const enqueue = (name: string, size = 100) =>
    tasks.enqueue({ kbId: 'kb1', kbName: 'KB', uploads: [{ file: file(name, size) }] })
  return { user, tenant, calls, tasks, enqueue }
}

test('replacing the user object for the same person keeps uploads running', () => {
  const { user, tenant, calls, tasks, enqueue } = setup()
  enqueue('a')
  // What setUser() does on every /auth/me refresh.
  user.value = { id: 'u1', nickname: 'refreshed' }
  assert.equal(tasks.items.length, 1)
  assert.equal(calls[0].signal.aborted, false)
  assert.equal(tasks.visible.value, true)

  tenant.value = 2
  assert.equal(tasks.items.length, 0)
  assert.equal(calls[0].signal.aborted, true)
  assert.equal(tasks.visible.value, false)
})

test('a new batch keeps an earlier batch that still has failures to retry', async () => {
  const { calls, tasks, enqueue } = setup()
  enqueue('broken', 1)
  calls[0].reject({ status: 500, message: 'boom' })
  await flush()
  assert.equal(tasks.summary.value.stage, 'done')

  enqueue('next', 2)
  assert.deepEqual(tasks.items.map(item => item.name), ['broken', 'next'])
  tasks.retryItem(tasks.items[0].id)
  assert.equal(calls.at(-1)!.payload.file.name, 'broken')
})

test('a new batch drops earlier batches that finished cleanly', async () => {
  const { calls, tasks, enqueue } = setup()
  enqueue('done', 1)
  calls[0].resolve({ success: true, data: { id: 'k1', parse_status: 'completed' } })
  await flush()

  enqueue('next', 2)
  assert.deepEqual(tasks.items.map(item => item.name), ['next'])
  assert.equal(tasks.batches.length, 1)
})
