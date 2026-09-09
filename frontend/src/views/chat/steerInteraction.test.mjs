import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { webcrypto } from 'node:crypto'
import vm from 'node:vm'
import test from 'node:test'
import { previewSteerMessage, discardSteerPreview, forkAfterInjectedUser } from '../../utils/steerStreamFork.ts'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const handlers = source.slice(source.indexOf('const dropSteerQueueItem ='), source.indexOf('let attachingSteerFollowUp ='))
const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function harness(overrides = {}) {
  const state = {
    session_id: { value: 'session' }, currentAssistantMessageId: { value: 'assistant' },
    isReplying: { value: true }, isStreaming: { value: true },
    steerQueue: { value: [] }, messagesList: [], crypto: webcrypto,
    previewSteerMessage, discardSteerPreview,
    scrollToBottom() {}, sendMsg() { throw new Error('must not start a second run') },
    MessagePlugin: { info() {}, error() {} }, t: key => key,
    console: { error() {} }, ...overrides,
  }
  const actions = vm.runInNewContext(`${handlers}\n({ handleSteerMsg, handlePromoteSteer, handleRetrySteer, handleRemoveSteer })`, state)
  return { ...actions, state }
}

test('default after stays below; promotion immediately moves it into the transcript', async () => {
  const request = deferred(), promote = deferred()
  const h = harness({ steerSession: () => request.promise, promoteSteerSession: () => promote.promise })
  const sending = h.handleSteerMsg('queued')
  const item = h.state.steerQueue.value[0]
  assert.equal(item.delivery, 'after')
  assert.equal(h.state.messagesList.length, 0)
  request.resolve({ status: 'queued', steer_id: item.steer_id })
  await sending
  const promoting = h.handlePromoteSteer(item.steer_id)
  assert.equal(item.delivery, 'inject')
  assert.equal(h.state.steerQueue.value.filter(q => q.delivery === 'after').length, 0)
  assert.equal(h.state.messagesList[0].content, 'queued')
  promote.resolve({ status: 'queued' })
  await promoting
})

test('direct inject shows immediately and an early SSE receipt does not duplicate it', async () => {
  const request = deferred()
  const h = harness({ steerSession: () => request.promise })
  const assistant = { id: 'assistant', role: 'assistant', request_id: 'request', is_completed: false }
  h.state.messagesList.push(assistant)
  const sending = h.handleSteerMsg('补充', [], 'inject')
  const item = h.state.steerQueue.value[0]
  assert.equal(h.state.messagesList[1].content, '补充')
  forkAfterInjectedUser(h.state.messagesList, assistant, h.state.messagesList[1], item.steer_id)
  h.state.steerQueue.value.splice(0, 1) // onUserMessageInjected receipt
  request.resolve({ status: 'queued', steer_id: item.steer_id })
  await sending
  assert.equal(h.state.messagesList.filter(m => m.role === 'user').length, 1)
  assert.equal(h.state.steerQueue.value.length, 0)
})

test('failed inject retries with the same ID and bubble', async () => {
  const ids = []
  const h = harness({ steerSession: async (...args) => {
    ids.push(args[5])
    if (ids.length === 1) throw new Error('network')
    return { status: 'queued', steer_id: args[5] }
  } })
  await h.handleSteerMsg('补充', [], 'inject')
  assert.equal(h.state.messagesList[0]._steerFailed, true)
  await h.handleRetrySteer(ids[0])
  assert.deepEqual(ids, [ids[0], ids[0]])
  assert.equal(h.state.messagesList.length, 1)
  assert.equal(h.state.messagesList[0]._steerFailed, undefined)
})

test('a lost HTTP response after a delivery receipt does not report a failed send', async () => {
  const request = deferred(), errors = []
  const h = harness({ steerSession: () => request.promise, MessagePlugin: { info() {}, error: message => errors.push(message) } })
  const sending = h.handleSteerMsg('补充', [], 'inject')
  h.state.steerQueue.value.splice(0, 1) // receipt already removed the item
  request.reject(new Error('response lost'))
  await sending
  assert.equal(errors.length, 0)
})

test('failed promotion restores the after queue and removes only its optimistic row', async () => {
  const h = harness({ promoteSteerSession: async () => { throw new Error('network') } })
  h.state.steerQueue.value.push({ steer_id: 'queued', content: '补充', delivery: 'after' })
  await h.handlePromoteSteer('queued')
  assert.equal(h.state.steerQueue.value[0].delivery, 'after')
  assert.equal(h.state.messagesList.length, 0)
})
