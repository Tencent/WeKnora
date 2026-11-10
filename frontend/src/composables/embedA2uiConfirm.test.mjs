import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

// The follow-up leg of an embed confirm click: handleA2uiConfirm plus the two
// ways of following the server-started run, sliced out of
// useEmbedChatSession.ts and run against stubs.
//
// The primary way is to ATTACH to the run's replay stream, through the
// embed-plane route GET /api/v1/embed/:channel_id/sessions/:session_id/
// continue-stream — exactly what the main plane does with
// /api/v1/sessions/continue-stream. That is the only way anything can appear
// while the run is going: the row's `content` stays "" until handleComplete
// persists it (completeAssistantMessage in internal/handler/session/qa.go), so a
// polled message list
// jumps from empty to finished and nothing in between.
// Polling the message list is kept as the give-up fallback, so a follow-up is
// never lost when the stream cannot be attached.
//
// alwaysStrict:false is load-bearing: the slice assigns to module-scope
// variables defined outside the window (`resumePoll`, `attachingFollowUpRun`),
// exactly like the real module.
const source = readFileSync(new URL('./useEmbedChatSession.ts', import.meta.url), 'utf8')
const BEGIN = source.indexOf('// EMBED_A2UI_CONFIRM_BEGIN')
const END = source.indexOf('// EMBED_A2UI_CONFIRM_END')
assert.ok(BEGIN >= 0 && END > BEGIN, 'the EMBED_A2UI_CONFIRM markers are missing from useEmbedChatSession.ts')
const fnStart = source.indexOf('const attachEmbedRun', BEGIN)
assert.ok(fnStart > BEGIN && fnStart < END, 'attachEmbedRun must start the marked window')
const logic = source.slice(fnStart, END)
assert.ok(
  logic.length > 400 && logic.includes('getEmbedMessageList') && logic.includes('handleA2uiConfirm'),
  'the sliced region no longer contains the confirm follow-up logic',
)
const compiled = ts.transpileModule(logic, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, alwaysStrict: false },
}).outputText

// The stub sleep resolves immediately, so a 60-attempt attach converges inside
// the test; the recorded intervals are what pin the cadence.
function harness(overrides = {}) {
  const polls = []
  const timers = []
  const toasts = []
  const streams = []
  const sleeps = []
  const sandbox = {
    options: {
      channelId: 'ch-1', token: 'tok', sessionId: { value: 'sess-1' },
      sessionSig: { value: 'sig-1' }, visitorId: { value: 'v-1' },
    },
    messagesList: [],
    isReplying: { value: false },
    loading: { value: false },
    currentAssistantMessageId: { value: '' },
    userHasScrolledUp: { value: true },
    limit: { value: 20 },
    error: { value: null },
    resumePoll: null,
    attachingFollowUpRun: false,
    stopResumePoll: () => {},
    scrollToBottom: () => {},
    resetAndLoad: (sid) => { sandbox.__reset.push(sid) },
    embedToast: (m, type) => { toasts.push({ m, type }) },
    sleep: async (ms) => { sleeps.push(ms); sandbox.__onSleep?.(sandbox, sleeps.length) },
    startStream: async (p) => {
      streams.push(p)
      if (sandbox.__streamFails) sandbox.error.value = 'HTTP 404'
    },
    getEmbedMessageList: async (...a) => { polls.push(a); return { data: sandbox.__list || [] } },
    __list: [],
    __reset: [],
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length },
    clearTimeout: () => {},
    Date,
  }
  Object.assign(sandbox, overrides)
  vm.runInNewContext(
    compiled + '\nthis.handleA2uiConfirm = handleA2uiConfirm;'
      + 'this.followConfirmRun = followConfirmRun; this.pollConfirmRun = pollConfirmRun;',
    sandbox,
  )
  return { sandbox, polls, timers, toasts, streams, sleeps }
}

const evt = (detail) => ({ detail })
const settled = () => new Promise((resolve) => setImmediate(resolve))

test('started: echoes the user bubble, lights the indicator and attaches to the run', async () => {
  const { sandbox, polls, streams, sleeps } = harness()
  sandbox.handleA2uiConfirm(evt({
    status: 'started', sessionId: 'sess-1', messageId: 'u-1',
    assistantMessageId: 'a-9', text: '按方案继续 姓名：张三 备注：未填写',
  }))
  assert.equal(sandbox.messagesList.length, 1)
  assert.equal(sandbox.messagesList[0].role, 'user')
  assert.equal(sandbox.messagesList[0].channel, 'a2ui')
  assert.equal(sandbox.messagesList[0].content, '按方案继续 姓名：张三 备注：未填写')
  assert.equal(sandbox.isReplying.value, true)
  assert.equal(sandbox.loading.value, true, 'no indicator during the follow-up means a blank panel')
  assert.equal(sandbox.currentAssistantMessageId.value, 'a-9')

  await settled()
  assert.equal(streams.length, 1, 'the follow-up must be followed on the stream, not by polling')
  assert.equal(polls.length, 0)
  assert.equal(sandbox.__reset.length, 0)
  // Spread into a host-realm object: the params were built inside the vm
  // context, so their prototype is not the host Object.prototype and
  // deepStrictEqual would reject them as "not reference-equal".
  assert.deepEqual({ ...streams[0] }, {
    session_id: 'sess-1',
    query: 'a-9',
    method: 'GET',
    url: '/api/v1/embed/ch-1/sessions',
    path_suffix: '/continue-stream',
    embed_token: 'tok',
    embed_session_sig: 'sig-1',
    embed_visitor_id: 'v-1',
  })
  assert.deepEqual(sleeps, [500], 'one attempt, after the run had a moment to start')
})

test('a successful attach stops the follow: no polling, no reload', async () => {
  const { sandbox, polls, streams, sleeps } = harness()
  await sandbox.followConfirmRun('a-9')
  assert.equal(streams.length, 1)
  assert.equal(sleeps.length, 1)
  assert.equal(polls.length, 0, 'an attached stream owns the message; polling it would only fight it')
  assert.deepEqual(sandbox.__reset, [], 'reloading the list right after attaching would repaint the run away')
})

test('the replay buffer is retried, and only a give-up falls back to polling', async () => {
  const { sandbox, polls, streams, timers, sleeps } = harness({ __streamFails: true })
  await sandbox.followConfirmRun('a-9')
  assert.equal(streams.length, 60, 'the run is kicked in a goroutine, so early 404s are expected')
  assert.equal(sleeps.length, 60)
  assert.equal(sleeps[0], 500)
  assert.equal(sandbox.attachingFollowUpRun, false, 'the attach window must close, or errors stay muted forever')

  assert.equal(polls.length, 0)
  assert.equal(timers.length, 1, 'giving up on the stream falls back to the message-list poll')
  sandbox.__list = [{ id: 'a-9', is_completed: false }]
  await timers[0].fn()
  assert.deepEqual(sandbox.__reset, [], 'a still-running message must not end the follow')
  sandbox.__list = [{ id: 'a-9', is_completed: true }]
  await timers[timers.length - 1].fn()
  assert.deepEqual(sandbox.__reset, ['sess-1'])
})

test('a not-yet-visible assistant message keeps the fallback poll alive (not a reset)', async () => {
  const { sandbox, timers } = harness({ __streamFails: true, __list: [{ id: 'other', is_completed: true }] })
  sandbox.pollConfirmRun('a-9')
  await timers[0].fn()
  assert.deepEqual(sandbox.__reset, [])
})

test('switching sessions mid-attach stops following the old run', async () => {
  const { sandbox, streams, polls } = harness({
    __onSleep: (sb, n) => { if (n === 1) sb.options.sessionId.value = 'sess-OTHER' },
  })
  await sandbox.followConfirmRun('a-9')
  assert.equal(streams.length, 0, 'a switched-away tab must not keep hitting the old session')
  assert.equal(polls.length, 0)
})

test('wrong session and odd statuses ignored; already_executed toasts info', async () => {
  const { sandbox, toasts, streams } = harness()
  sandbox.handleA2uiConfirm(evt({ status: 'started', sessionId: 'sess-OTHER', messageId: 'u', assistantMessageId: 'a', text: 'x' }))
  sandbox.handleA2uiConfirm(evt({ status: 'nonsense' }))
  sandbox.handleA2uiConfirm(evt({ status: 'already_executed', sessionId: 'sess-1' }))
  await settled()
  assert.equal(sandbox.messagesList.length, 0)
  assert.equal(sandbox.isReplying.value, false)
  assert.equal(streams.length, 0)
  assert.equal(toasts.length, 1)
  assert.equal(toasts[0].type, 'info')
  assert.equal(toasts[0].m, '已确认，无需重复操作') // literal, same copy as the main plane
})

test('a running turn refuses a new confirm with a warning and touches nothing', async () => {
  const { sandbox, toasts, streams } = harness({ isReplying: { value: true } })
  sandbox.handleA2uiConfirm(evt({ status: 'started', sessionId: 'sess-1', messageId: 'u', assistantMessageId: 'a', text: 'x' }))
  await settled()
  assert.equal(sandbox.messagesList.length, 0)
  assert.equal(streams.length, 0)
  assert.equal(toasts[0].m, '请等本轮回答结束后再确认')
})

test('the error watcher mutes failures raised while an attach is in flight', () => {
  // The watcher lives outside the sliced window (it is registered on the module
  // scope), so it is pinned against the source itself. Without the flag, every
  // expected 404 of the 60-attempt window would toast, and the first one would
  // also clear isReplying/loading — the indicator this fix exists to light.
  const w = source.slice(source.indexOf('watch(error,'))
  const guard = w.indexOf('attachingFollowUpRun')
  const toast = w.indexOf('embedToast(newError)')
  assert.ok(guard >= 0, 'watch(error) must consult attachingFollowUpRun')
  assert.ok(toast >= 0 && guard < toast, 'the guard must come before any toast or state reset')
})
