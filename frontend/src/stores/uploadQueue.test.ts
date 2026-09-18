import assert from 'node:assert/strict'
import test from 'node:test'
import {
  createUploadQueue,
  type KnowledgeStatusRow,
  type UploadBatch,
  type UploadPayload,
  type UploadQueueDeps,
} from './uploadQueue.ts'
import type { UploadItem } from './uploadTasksState.ts'

type Deferred = {
  payload: UploadPayload
  signal: AbortSignal
  progress: (ratio: number) => void
  resolve: (value: unknown) => void
  reject: (reason: unknown) => void
}

const file = (name: string, size = 100) => ({ name, size, webkitRelativePath: '' }) as unknown as File

const flush = () => new Promise(resolve => setImmediate(resolve))

function setup(overrides: Partial<UploadQueueDeps> = {}) {
  const items: UploadItem[] = []
  const batches: UploadBatch[] = []
  const calls: Deferred[] = []
  const uploaded: string[] = []
  const timers: Array<() => void> = []
  const statusRows = new Map<string, KnowledgeStatusRow[] | null>()

  const queue = createUploadQueue(items, batches, {
    concurrency: 2,
    pollIntervalMs: 1000,
    pollChunkSize: 2,
    upload: (_batch, payload, progress, signal) => new Promise((resolve, reject) => {
      calls.push({ payload, signal, progress, resolve, reject })
      signal.addEventListener('abort', () => reject({ message: 'aborted' }))
    }),
    queryStatus: async (kbId, ids) => {
      const rows = statusRows.get(kbId)
      return rows === null ? null : (rows ?? []).filter(row => ids.includes(row.id))
    },
    onUploaded: kbId => uploaded.push(kbId),
    setTimer: fn => {
      timers.push(fn)
      return timers.length
    },
    clearTimer: () => {},
    ...overrides,
  })
  const batch = { kbId: 'kb1', kbName: 'KB', targetFolder: '' }
  const byName = (name: string) => items.find(item => item.name === name)!
  return { items, batches, calls, uploaded, timers, statusRows, queue, batch, byName }
}

test('runs at most `concurrency` transfers and starts the next as one settles', async () => {
  const { calls, queue, batch, byName } = setup()
  queue.add(batch, [{ file: file('a') }, { file: file('b') }, { file: file('c') }])
  assert.deepEqual(calls.map(call => call.payload.file.name), ['a', 'b'])
  assert.equal(byName('c').transfer, 'queued')

  calls[0].progress(0.4)
  assert.equal(byName('a').loaded, 40)

  calls[0].resolve({ success: true, data: { id: 'k-a', parse_status: 'pending' } })
  await flush()
  assert.equal(byName('a').transfer, 'uploaded')
  assert.equal(byName('a').knowledgeId, 'k-a')
  assert.deepEqual(calls.map(call => call.payload.file.name), ['a', 'b', 'c'])
})

test('classifies duplicates and failures, and retries a failed file', async () => {
  const { calls, uploaded, queue, batch, byName } = setup()
  queue.add(batch, [{ file: file('dup') }, { file: file('bad') }])

  calls[0].reject({ status: 409, code: 'duplicate_file', data: { id: 'k-old' } })
  calls[1].reject({ status: 400, message: 'too large' })
  await flush()
  assert.equal(byName('dup').transfer, 'duplicate')
  assert.equal(byName('dup').knowledgeId, 'k-old')
  assert.equal(byName('bad').transfer, 'failed')
  assert.equal(byName('bad').error, 'too large')
  assert.deepEqual(uploaded, [])

  queue.retryFailed()
  assert.equal(calls.length, 3)
  assert.equal(calls[2].payload.file.name, 'bad')
  // Duplicates are settled, not retryable.
  assert.equal(byName('dup').transfer, 'duplicate')
  calls[2].resolve({ success: true, data: { id: 'k-bad' } })
  await flush()
  assert.equal(byName('bad').transfer, 'uploaded')
  assert.equal(byName('bad').parseStatus, 'pending')
  assert.deepEqual(uploaded, ['kb1'])
})

test('cancelling aborts the request without reporting it as a failure', async () => {
  const { calls, queue, batch, byName } = setup()
  queue.add(batch, [{ file: file('a') }, { file: file('b') }, { file: file('c') }])

  queue.cancel(byName('c').id)
  assert.equal(byName('c').transfer, 'cancelled')

  queue.cancel(byName('a').id)
  assert.equal(calls[0].signal.aborted, true)
  await flush()
  assert.equal(byName('a').transfer, 'cancelled')
  assert.equal(byName('a').error, undefined)
  // The freed slot doesn't resurrect the cancelled queued file.
  assert.equal(calls.length, 2)

  // Fully sent: the server is already storing it, so it can't be cancelled.
  calls[1].progress(1)
  queue.cancel(byName('b').id)
  assert.equal(calls[1].signal.aborted, false)
  assert.equal(byName('b').transfer, 'uploading')
})

test('polls created rows until parsing settles', async () => {
  const { calls, timers, statusRows, queue, batch, byName } = setup()
  queue.add(batch, [{ file: file('a') }, { file: file('b') }, { file: file('c') }])
  calls[0].resolve({ success: true, data: { id: 'k-a', parse_status: 'pending' } })
  calls[1].resolve({ success: true, data: { id: 'k-b', parse_status: 'pending' } })
  await flush()
  calls[2].resolve({ success: true, data: { id: 'k-c', parse_status: 'pending' } })
  await flush()
  // One timer no matter how many uploads landed.
  assert.equal(timers.length, 1)

  statusRows.set('kb1', null)
  await queue.pollNow()
  assert.equal(byName('a').parseStatus, 'pending', 'a failed request changes nothing')

  statusRows.set('kb1', [
    { id: 'k-a', parse_status: 'completed' },
    { id: 'k-b', parse_status: 'failed', error_message: 'broken pdf' },
  ])
  await queue.pollNow()
  assert.equal(byName('a').parseStatus, 'completed')
  assert.equal(byName('b').parseStatus, 'failed')
  assert.equal(byName('b').parseError, 'broken pdf')
  assert.equal(byName('c').parseStatus, 'deleted')
})

test('clear() forgets everything and ignores transfers that finish afterwards', async () => {
  const { items, batches, calls, uploaded, queue, batch } = setup()
  queue.add(batch, [{ file: file('a') }])
  const first = calls[0]
  first.progress(1)

  queue.clear()
  assert.equal(first.signal.aborted, true)
  assert.equal(items.length, 0)
  assert.equal(batches.length, 0)

  queue.add(batch, [{ file: file('next') }])
  first.resolve({ success: true, data: { id: 'late' } })
  await flush()
  assert.equal(items.length, 1)
  assert.equal(items[0].name, 'next')
  assert.deepEqual(uploaded, [])
})
