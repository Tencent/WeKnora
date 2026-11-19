import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

// The attach leg of the confirm flow, exercised without a browser: the
// handleA2uiConfirm handler is sliced out of index.vue <script setup>,
// transpiled and run against stubs (same idiom as A2uiCard.action.test.mjs).
//
// alwaysStrict:false is load-bearing: the handler assigns to the
// module-scoped `attachingResumedRun` (defined OUTSIDE the slice), so the
// transpiled code contains a bare assignment. ts.transpileModule's default
// "use strict" turns that into a ReferenceError in the sandbox; sloppy mode
// lets the assignment land on the sandbox binding, the way module scope does
// in the real build.
// ../../ — this file lives in components/a2ui/, index.vue lives in views/chat/
const source = readFileSync(new URL('../../index.vue', import.meta.url), 'utf8')
const HANDLER_START = 'const handleA2uiConfirm = async'
const REGISTER = "window.addEventListener(A2UI_CONFIRM_EVENT, handleA2uiConfirm)"
const start = source.indexOf(HANDLER_START)
const endIdx = source.indexOf(REGISTER)
assert.ok(start >= 0, 'handleA2uiConfirm not found in index.vue')
assert.ok(endIdx > start, 'the A2UI_CONFIRM_EVENT registration is not right after the handler')
const logic = source.slice(start, endIdx + REGISTER.length)
assert.ok(
  logic.length > 200 && logic.includes('startStream(') && logic.includes('attachingResumedRun'),
  'handleA2uiConfirm no longer contains the attach loop this test pins',
)
const compiled = ts.transpileModule(logic, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, alwaysStrict: false },
}).outputText

function harness(overrides = {}) {
  const started = []
  const toasts = { info: [], warning: [], error: [] }
  const sandbox = {
    A2UI_CONFIRM_EVENT: 'weknora:a2ui-confirm',
    session_id: { value: 'sess-1' },
    isReplying: { value: false },
    loading: { value: false },
    messagesList: [],
    userHasScrolledUp: { value: true },
    scrollToBottom: () => {},
    currentAssistantMessageId: { value: '' },
    attachingResumedRun: false,
    MessagePlugin: {
      info: (m) => toasts.info.push(m),
      warning: (m) => toasts.warning.push(m),
      error: (m) => toasts.error.push(m),
    },
    startStream: async (opts) => { started.push(opts) },
    error: { value: null },
    window: {
      listeners: {},
      addEventListener: (type, fn) => { sandbox.window.listeners[type] = fn },
      removeEventListener: () => {},
    },
    setTimeout,
    ...overrides,
  }
  vm.runInNewContext(compiled, sandbox)
  return { started, toasts, sandbox }
}

function fire(h, detail) {
  const fn = h.sandbox.window.listeners['weknora:a2ui-confirm']
  assert.ok(typeof fn === 'function', 'the A2UI_CONFIRM_EVENT listener must be registered')
  return fn({ detail })
}

test('a started confirm pushes the user row and attaches to the run stream', async () => {
  const h = harness()
  await fire(h, { status: 'started', sessionId: 'sess-1', messageId: 'user-9', assistantMessageId: 'asst-9', text: '按方案继续' })

  assert.equal(h.sandbox.messagesList.length, 1)
  const row = h.sandbox.messagesList[0]
  assert.equal(row.id, 'user-9')
  assert.equal(row.content, '按方案继续')
  assert.equal(row.role, 'user')
  assert.equal(row.channel, 'a2ui')
  assert.equal(typeof row.created_at, 'string')
  assert.equal(h.sandbox.userHasScrolledUp.value, false)
  assert.equal(h.sandbox.isReplying.value, true)
  assert.equal(h.sandbox.currentAssistantMessageId.value, 'asst-9')
  assert.equal(h.sandbox.attachingResumedRun, false, 'the flag must be released in finally')
  // JSON round-trip: the opts object crosses the vm realm (different Object
  // prototype breaks strict deepEqual), same idiom as the body assertion in
  // A2uiCard.action.test.mjs.
  assert.deepEqual(JSON.parse(JSON.stringify(h.started)), [
    { session_id: 'sess-1', query: 'asst-9', method: 'GET', url: '/api/v1/sessions/continue-stream' },
  ])
  assert.equal(h.toasts.info.length + h.toasts.warning.length + h.toasts.error.length, 0)
})

test('a confirm for another session is ignored', async () => {
  const h = harness()
  await fire(h, { status: 'started', sessionId: 'sess-2', messageId: 'user-9', assistantMessageId: 'asst-9', text: 'x' })
  assert.equal(h.sandbox.messagesList.length, 0)
  assert.equal(h.started.length, 0)
  assert.equal(h.sandbox.isReplying.value, false)
})

test('a confirm while the view is already replying is refused with a warning', async () => {
  const h = harness({ isReplying: { value: true } })
  await fire(h, { status: 'started', sessionId: 'sess-1', messageId: 'user-9', assistantMessageId: 'asst-9', text: 'x' })
  assert.equal(h.sandbox.messagesList.length, 0)
  assert.equal(h.started.length, 0)
  assert.deepEqual(h.toasts.warning, ['请等本轮回答结束后再确认'])
})

test('already_executed only toasts, never touches the stream', async () => {
  const h = harness()
  await fire(h, { status: 'already_executed', sessionId: 'sess-1', messageId: 'user-9', assistantMessageId: '', text: '' })
  assert.equal(h.sandbox.messagesList.length, 0)
  assert.equal(h.started.length, 0)
  assert.deepEqual(h.toasts.info, ['已确认，无需重复操作'])
  assert.equal(h.sandbox.isReplying.value, false)
})
