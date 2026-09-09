import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { webcrypto } from 'node:crypto'
import vm from 'node:vm'
import test from 'node:test'
import { makeSteerClientId } from '../../utils/steerId.ts'
import ts from 'typescript'
import { reactive } from 'vue'
import { previewSteerMessage, discardSteerPreview, forkAfterInjectedUser, reconcileSteerMessageId } from '../../utils/steerStreamFork.ts'

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
    steerQueue: { value: [] }, messagesList: reactive([]), crypto: webcrypto, makeSteerClientId,
    previewSteerMessage, discardSteerPreview, reconcileSteerMessageId,
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

const streamSource = readFileSync(new URL('../../composables/useChatStreamHandler.ts', import.meta.url), 'utf8')
const receiptStart = streamSource.indexOf("case 'user_message_injected': {")
const receiptEnd = streamSource.indexOf("case 'complete':", receiptStart)
assert.ok(receiptStart >= 0 && receiptEnd > receiptStart)
const receipt = ts.transpile(`() => { switch ('user_message_injected') {
  ${streamSource.slice(receiptStart, receiptEnd)}
} }`)

function receiveInjection(h, steerId, userId) {
  vm.runInNewContext(receipt, {
    messagesList: h.state.messagesList,
    message: h.state.messagesList.findLast(m => m.role === 'assistant'),
    dataPayload: { steer_id: steerId, user_message_id: userId, content: '写到Docx' },
    data: {}, dataId: 'request', replaySegments: new Map(),
    forkAfterInjectedUser, log() {}, emitMessageCreated() {}, onAgentChunkBound() {},
    onUserMessageInjected(id) {
      const index = h.state.steerQueue.value.findIndex(item => item.steer_id === id)
      if (index >= 0) h.state.steerQueue.value.splice(index, 1)
    },
  })()
}

for (const receiptFirst of [false, true]) {
  test(`server-assigned steer ID reconciles the optimistic bubble when SSE arrives ${receiptFirst ? 'before' : 'after'} HTTP`, async () => {
    const request = deferred()
    const h = harness({ steerSession: () => request.promise })
    h.state.messagesList.push({ id: 'assistant', role: 'assistant', request_id: 'request', is_completed: false })
    const sending = h.handleSteerMsg('写到Docx', [{ id: 'mention' }], 'inject')
    if (receiptFirst) receiveInjection(h, 'server-steer', 'persisted-user')
    request.resolve({ status: 'queued', steer_id: 'server-steer' })
    await sending
    if (!receiptFirst) receiveInjection(h, 'server-steer', 'persisted-user')
    const users = h.state.messagesList.filter(m => m.role === 'user')
    assert.equal(users.length, 1)
    assert.equal(users[0].id, 'persisted-user')
    assert.equal(users[0]._steerPending, undefined)
    assert.equal(users[0].mentioned_items[0].id, 'mention')
    assert.equal(h.state.steerQueue.value.length, 0)
    assert.deepEqual(h.state.messagesList.map(m => m.role), ['assistant', 'user', 'assistant'])
  })
}

test('concurrent identical injects reconcile by their HTTP receipts without merging distinct messages', async () => {
  const first = deferred(), second = deferred()
  let calls = 0
  const h = harness({ steerSession: () => (++calls === 1 ? first.promise : second.promise) })
  h.state.messagesList.push({ id: 'assistant', role: 'assistant', request_id: 'request', is_completed: false })
  const sendingFirst = h.handleSteerMsg('写到Docx', [{ id: 'first' }], 'inject')
  const sendingSecond = h.handleSteerMsg('写到Docx', [{ id: 'second' }], 'inject')
  receiveInjection(h, 'server-first', 'user-first')
  receiveInjection(h, 'server-second', 'user-second')
  second.resolve({ status: 'queued', steer_id: 'server-second' })
  await sendingSecond
  first.resolve({ status: 'queued', steer_id: 'server-first' })
  await sendingFirst
  const users = h.state.messagesList.filter(m => m.role === 'user')
  assert.deepEqual(users.map(m => [m.id, m.mentioned_items[0].id]), [['user-first', 'first'], ['user-second', 'second']])
  assert.equal(h.state.steerQueue.value.length, 0)
})
