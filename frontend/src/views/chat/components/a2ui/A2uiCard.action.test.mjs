import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

// The click → POST leg of route B, exercised without a browser: the onEvent
// callback is sliced out of <script setup>, transpiled and run against a stub
// post(). Same idiom as SkillInstallTimeline.test.mjs (no jsdom in this repo).
//
// Why this file exists at all: the first live run in a real deployment
// (2026-11-05) posted to "/agent/actions/execute" — the axios instance's
// baseURL is import.meta.env.BASE_URL, "/" in the Docker build, so a bare path
// lands on the SPA origin and nginx answers 405 for POST. Nothing in the unit
// suite could see it, because every other assertion about this leg was about
// the frames, never about the URL. Hence: the expected path below is a
// literal, deliberately NOT imported from the component or from any shared
// constant — a pin that reads the same symbol as the code under test cannot
// fail when the code drops its prefix.
const source = readFileSync(new URL('./A2uiCard.vue', import.meta.url), 'utf8')
// lastIndexOf, not indexOf: `onBeforeUnmount` also appears in the import list
// at the top of <script setup>, so the first hit is before the slice start.
const logic = source.slice(
  source.indexOf('const unsubscribe = processor.onEvent'),
  source.lastIndexOf('onBeforeUnmount'),
)
const compiled = ts.transpileModule(logic, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

// Load-time guard: an empty or mis-sliced `logic` would leave every test below
// green while asserting nothing (the stubbed post() would simply never be called
// and `calls.length === 1` would fail — but a rename that moved the handler out
// of the slice would otherwise be reported as four unrelated failures).
assert.ok(
  logic.length > 200 && logic.includes('processor.onEvent') && logic.includes('post('),
  'A2uiCard.vue no longer exposes the click handler where this test slices it',
)

const ENVELOPE = {
  message: {
    action: {
      name: 'noop',
      surfaceId: 'upgrade-confirm',
      sourceComponentId: 'b1',
      timestamp: '2026-11-05T01:16:00Z',
      context: { k: 'v' },
    },
  },
}

// A confirm button click: the envelope carries the persisted context, but
// the server response is what drives the attach (and its message text).
const CONFIRM_ENVELOPE = {
  message: {
    action: {
      name: 'confirm',
      surfaceId: 's2',
      sourceComponentId: 'ok',
      timestamp: '2026-11-06T01:00:00Z',
      context: { message: '按方案继续' },
    },
  },
}

function harness(overrides = {}) {
  const calls = []
  const errors = []
  const captured = {}
  const events = []
  const sandbox = {
    props: { sessionId: 'sess-1', messageId: 'msg-1' },
    reflowFailed: { value: false },
    processor: {
      onEvent: (handler) => {
        captured.handler = handler
        return () => { captured.unsubscribed = true }
      },
    },
    post: async (url, body) => {
      calls.push({ url, body })
      return {}
    },
    // The real post() resolves to the response PAYLOAD (the axios interceptor
    // returns data, not response); the {} default models any pre-confirm
    // response shape (no data.status → no event).
    A2UI_CONFIRM_EVENT: 'weknora:a2ui-confirm',
    CustomEvent: class CustomEvent {
      constructor(type, init) { this.type = type; this.detail = init?.detail }
    },
    window: { dispatchEvent: (e) => { events.push(e); return true } },
    console: { error: (...args) => errors.push(args) },
    ...overrides,
  }
  vm.runInNewContext(compiled, sandbox)
  return { calls, errors, captured, events, sandbox }
}

test('a card click posts the official action envelope to the versioned API path', async () => {
  const h = harness()
  await h.captured.handler(ENVELOPE)

  assert.equal(h.calls.length, 1, 'one click must produce exactly one request')
  assert.equal(h.calls[0].url, '/api/v1/agent/actions/execute')
  assert.ok(
    h.calls[0].url.startsWith('/api/v1/'),
    'the SPA origin serves the static bundle at the bare path, so a request '
    + 'without the API prefix is answered 405 by nginx before any auth runs',
  )
})

test('the request body carries the server-contract field names, not the envelope', async () => {
  const h = harness()
  await h.captured.handler(ENVELOPE)

  // JSON round-trip: the payload crosses the vm realm (different Object
  // prototype → deepStrictEqual would fail on structureless equality) and this
  // is also the shape actually serialized onto the wire.
  assert.deepEqual(JSON.parse(JSON.stringify(h.calls[0].body)), {
    session_id: 'sess-1',
    message_id: 'msg-1',
    surface_id: 'upgrade-confirm',
    source_component_id: 'b1',
    action_name: 'noop',
    context: { k: 'v' },
  })
})

test('an action without context still sends an object, never undefined', async () => {
  const h = harness()
  const bare = { message: { action: { ...ENVELOPE.message.action, context: undefined } } }
  await h.captured.handler(bare)

  // Serialised form, so `undefined` (which axios drops from the body) fails here
  // instead of quietly comparing equal to an empty object.
  assert.equal(JSON.stringify(h.calls[0].body.context), '{}')
})

test('a refused action leaves the card, marks it failed and logs once', async () => {
  const errorsSeen = []
  const h = harness({
    post: async (url, body) => {
      errorsSeen.push({ url, body })
      throw Object.assign(new Error('Request failed with status code 405'), { status: 405 })
    },
  })
  await h.captured.handler(ENVELOPE)
  // The .catch() is not awaited by the handler; let the microtask queue drain.
  await new Promise((resolve) => { setTimeout(resolve, 0) })

  assert.equal(errorsSeen.length, 1, 'the request was attempted')
  assert.equal(h.sandbox.reflowFailed.value, true, 'the card must fall back to its source fence')
  assert.equal(h.errors.length, 1, 'the failure is logged exactly once')
})

test('a confirm click that starts a run dispatches the attach event with the server ids', async () => {
  const h = harness({
    post: async () => ({ success: true, data: { status: 'started', action_name: 'confirm', label: '确定', message: '按方案继续', message_id: 'user-9', assistant_message_id: 'asst-9' } }),
  })
  await h.captured.handler(CONFIRM_ENVELOPE)
  // The handler does not await post(); drain the microtask queue.
  await new Promise((resolve) => { setTimeout(resolve, 0) })

  assert.equal(h.events.length, 1, 'exactly one attach event')
  assert.equal(h.events[0].type, 'weknora:a2ui-confirm')
  assert.deepEqual(JSON.parse(JSON.stringify(h.events[0].detail)), {
    status: 'started',
    sessionId: 'sess-1',
    messageId: 'user-9',
    assistantMessageId: 'asst-9',
    text: '按方案继续',
  })
  assert.equal(h.sandbox.reflowFailed.value, false, 'a started run is not a refusal')
})

test('an already_executed confirm dispatches the event and never reflows', async () => {
  const h = harness({
    post: async () => ({ success: true, data: { status: 'already_executed', action_name: 'confirm', label: '确定' } }),
  })
  await h.captured.handler(CONFIRM_ENVELOPE)
  await new Promise((resolve) => { setTimeout(resolve, 0) })

  assert.equal(h.events.length, 1)
  assert.equal(h.events[0].type, 'weknora:a2ui-confirm')
  assert.equal(h.events[0].detail.status, 'already_executed')
  assert.equal(h.sandbox.reflowFailed.value, false)
  assert.equal(h.errors.length, 0)
})

test('a 409 while a run is in progress is not a card failure', async () => {
  const h = harness({
    post: async () => {
      throw Object.assign(new Error('Request failed with status code 409'), { status: 409 })
    },
  })
  await h.captured.handler(CONFIRM_ENVELOPE)
  await new Promise((resolve) => { setTimeout(resolve, 0) })

  assert.equal(h.events.length, 0, 'no attach event on a refused click')
  assert.equal(h.sandbox.reflowFailed.value, false, 'a busy session is not a reflow failure')
  assert.equal(h.errors.length, 0, 'a 409 must not log a reflow error')
})
