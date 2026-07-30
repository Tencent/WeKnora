import assert from 'node:assert/strict'
import test from 'node:test'

import {
  createChatFeedbackStateController,
  type ChatFeedbackIntent,
  type ChatFeedbackValue,
} from './chatFeedbackState.ts'

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
}

const nextMicrotask = () => new Promise<void>(resolve => queueMicrotask(resolve))

async function waitFor(condition: () => boolean) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    if (condition()) return
    await nextMicrotask()
  }
  assert.ok(condition(), 'condition did not become true')
}

function createRecorder(overrides: {
  read?: (messageId: string) => Promise<ChatFeedbackValue>
  submit?: (messageId: string, isPositive: boolean, dislikeReason?: string) => Promise<void>
  cancel?: (messageId: string) => Promise<void>
} = {}) {
  const values: ChatFeedbackValue[] = []
  const pending: boolean[] = []
  const successes: ChatFeedbackIntent[] = []
  const errors: unknown[] = []
  const controller = createChatFeedbackStateController({
    read: overrides.read || (async () => null),
    submit: overrides.submit || (async () => {}),
    cancel: overrides.cancel || (async () => {}),
    onValueChange: value => values.push(value),
    onPendingChange: value => pending.push(value),
    onMutationSuccess: intent => successes.push(intent),
    onMutationError: error => errors.push(error),
  })
  return { controller, values, pending, successes, errors }
}

test('a read started before a mutation cannot overwrite the successful value', async () => {
  const staleRead = deferred<ChatFeedbackValue>()
  const state = createRecorder({ read: async () => staleRead.promise })
  state.controller.setMessage('message-1')

  const load = state.controller.load()
  await state.controller.request({ value: true })
  staleRead.resolve(false)
  await load

  assert.deepEqual(state.values, [null, true])
  assert.deepEqual(state.pending, [true, false])
  assert.deepEqual(state.successes, [{ value: true, dislikeReason: undefined }])
})

test('batch-hydrated history seeds feedback without a per-message read', () => {
  let reads = 0
  const state = createRecorder({
    read: async () => {
      reads += 1
      return null
    },
  })

  state.controller.setMessage('message-1', false)

  assert.equal(reads, 0)
  assert.deepEqual(state.values, [false])
})

test('rapid feedback changes are serialized and only the last intent reaches the UI', async () => {
  const first = deferred<void>()
  const second = deferred<void>()
  const requests = [first, second]
  const calls: Array<{ value: boolean; reason?: string }> = []
  let activeRequests = 0
  let maximumActiveRequests = 0

  const state = createRecorder({
    submit: async (_messageId, value, reason) => {
      const request = requests[calls.length]
      calls.push({ value, reason })
      activeRequests += 1
      maximumActiveRequests = Math.max(maximumActiveRequests, activeRequests)
      try {
        await request.promise
      } finally {
        activeRequests -= 1
      }
    },
  })
  state.controller.setMessage('message-1')

  const like = state.controller.request({ value: true })
  const dislike = state.controller.request({ value: false, dislikeReason: 'irrelevant' })
  assert.deepEqual(calls, [{ value: true, reason: undefined }])
  assert.equal(maximumActiveRequests, 1)

  first.resolve()
  await waitFor(() => calls.length === 2)
  assert.deepEqual(calls, [
    { value: true, reason: undefined },
    { value: false, reason: 'irrelevant' },
  ])
  assert.deepEqual(state.values, [null])

  second.resolve()
  await Promise.all([like, dislike])

  assert.equal(maximumActiveRequests, 1)
  assert.deepEqual(state.values, [null, false])
  assert.deepEqual(state.successes, [{ value: false, dislikeReason: 'irrelevant' }])
  assert.deepEqual(state.pending, [true, false])
})

test('only the last queued intent survives while an earlier mutation is active', async () => {
  const submitRequest = deferred<void>()
  const cancelRequest = deferred<void>()
  const calls: string[] = []
  let activeRequests = 0
  let maximumActiveRequests = 0
  const track = async (name: string, promise: Promise<void>) => {
    calls.push(name)
    activeRequests += 1
    maximumActiveRequests = Math.max(maximumActiveRequests, activeRequests)
    try {
      await promise
    } finally {
      activeRequests -= 1
    }
  }

  const state = createRecorder({
    submit: async (_messageId, value) => {
      if (!value) {
        calls.push('dislike')
        throw new Error('superseded dislike should not run')
      }
      return track('like', submitRequest.promise)
    },
    cancel: async () => track('cancel', cancelRequest.promise),
  })
  state.controller.setMessage('message-1')

  const like = state.controller.request({ value: true })
  const dislike = state.controller.request({ value: false, dislikeReason: 'irrelevant' })
  const cancel = state.controller.request({ value: null })
  assert.deepEqual(calls, ['like'])

  submitRequest.resolve()
  await waitFor(() => calls.length === 2)
  assert.deepEqual(calls, ['like', 'cancel'])

  cancelRequest.resolve()
  await Promise.all([like, dislike, cancel])

  assert.equal(maximumActiveRequests, 1)
  assert.deepEqual(state.successes, [{ value: null, dislikeReason: undefined }])
  assert.deepEqual(state.pending, [true, false])
})

test('a response for the previous message cannot update the active message', async () => {
  const oldRead = deferred<ChatFeedbackValue>()
  const newRead = deferred<ChatFeedbackValue>()
  const reads = [oldRead, newRead]
  let calls = 0
  const state = createRecorder({ read: async () => reads[calls++].promise })

  state.controller.setMessage('message-1')
  const oldLoad = state.controller.load()
  state.controller.setMessage('message-2')
  const newLoad = state.controller.load()

  newRead.resolve(true)
  await newLoad
  oldRead.resolve(false)
  await oldLoad

  assert.deepEqual(state.values, [null, null, true])
})
