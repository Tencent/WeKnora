import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { persistedAssistantId } from '../utils/steerStreamFork.ts'

// The embed plane's ask_user / tool-approval resume leg: followResumedRun and
// its give-up poll, sliced out of useEmbedChatSession.ts and run against stubs.
//
// The attach itself (attachEmbedRun, its URL and its 60 x 500ms cadence) is
// pinned by embedA2uiConfirm.test.mjs. What this file owns is the two things the
// resume leg adds on top: it must go through that attach AT ALL — polling the
// message list cannot show a run in progress, because the row's content stays
// "" until the completion path persists it — and it must forward the pending_id,
// without which ContinueStream answers 409 once the run is suspended
// (ContinueStream in internal/handler/session/stream.go) and the follow-up never
// attaches.
const source = readFileSync(new URL('./useEmbedChatSession.ts', import.meta.url), 'utf8')
const BEGIN = source.indexOf('// EMBED_A2UI_RESUME_BEGIN')
const END = source.indexOf('// EMBED_A2UI_RESUME_END')
assert.ok(BEGIN >= 0 && END > BEGIN, 'the EMBED_A2UI_RESUME markers are missing from useEmbedChatSession.ts')
const fnStart = source.indexOf('const followResumedRun', BEGIN)
assert.ok(fnStart > BEGIN && fnStart < END, 'followResumedRun must start the marked window')
const logic = source.slice(fnStart, END)
assert.ok(
  logic.includes('pollResumedRun') && logic.includes('getEmbedMessageList'),
  'the sliced region no longer contains the resume follow-up logic',
)
const compiled = ts.transpileModule(logic, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, alwaysStrict: false },
}).outputText

function harness(overrides = {}) {
  const attaches = []
  const polls = []
  const timers = []
  const prepared = []
  const resets = []
  const warns = []
  const sandbox = {
    console: { warn: (...a) => { warns.push(a) }, log: () => {}, error: () => {} },
    options: {
      channelId: 'ch-1', token: 'tok', sessionId: { value: 'sess-1' },
      sessionSig: { value: 'sig-1' }, visitorId: { value: 'v-1' },
    },
    messagesList: [{
      id: 'a-1', role: 'assistant',
      agentEventStream: [{ type: 'agent_prompt_required', pending_id: 'pend-7' }],
    }],
    isReplying: { value: false },
    loading: { value: false },
    currentAssistantMessageId: { value: '' },
    limit: { value: 20 },
    resumePoll: null,
    attachingFollowUpRun: false,
    // Same two card types as PROMPT_CARD_TYPES in useChatStreamHandler.ts.
    isPromptCard: (e) => e?.type === 'agent_prompt_required' || e?.type === 'tool_approval_required',
    // The real helper goes in, not a stub of it: a stub written from my own
    // reading of steerStreamFork would pass whatever the composable does.
    persistedAssistantId,
    prepareResumedMessage: (message, pendingId) => { prepared.push({ id: message.id, pendingId }) },
    stopResumePoll: () => { sandbox.__stopCalls++ },
    resetAndLoad: (sid) => { resets.push(sid) },
    attachEmbedRun: async (messageId, pendingId) => {
      attaches.push({ messageId, pendingId })
      return !sandbox.__attachFails
    },
    getEmbedMessageList: async (...a) => { polls.push(a); return { data: sandbox.__list || [] } },
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length },
    clearTimeout: () => {},
    Date,
    __list: [],
    __stopCalls: 0,
    __attachFails: false,
  }
  Object.assign(sandbox, overrides)
  vm.runInNewContext(compiled + '\nthis.followResumedRun = followResumedRun;'
    + 'this.pollResumedRun = pollResumedRun;', sandbox)
  return { sandbox, attaches, polls, timers, prepared, resets, warns }
}

test('an answered prompt attaches to the resumed run and carries the pending_id', async () => {
  const { sandbox, attaches, polls, prepared, resets } = harness()
  await sandbox.followResumedRun('pend-7')
  assert.deepEqual(prepared, [{ id: 'a-1', pendingId: 'pend-7' }])
  assert.equal(sandbox.isReplying.value, true)
  assert.equal(sandbox.loading.value, true, 'no indicator while the run resumes means a frozen card')
  assert.equal(sandbox.currentAssistantMessageId.value, 'a-1', 'the stop button targets this id')
  assert.deepEqual(attaches, [{ messageId: 'a-1', pendingId: 'pend-7' }],
    'no pending_id and the delegate 409s; no attach at all and nothing streams')
  assert.equal(polls.length, 0, 'an attached stream owns the message')
  assert.deepEqual(resets, [])
  assert.ok(sandbox.__stopCalls >= 1, 'a stale poll timer must not survive the resume')
  assert.equal(sandbox.attachingFollowUpRun, false, 'the attach window must close')
})

test('a row that carries the server-side assistant id attaches to that id', async () => {
  // What the main plane does with persistedAssistantId: the id the server knows
  // this message by is the one the replay buffer is keyed on. Reading `id`
  // straight would ask for a message nothing is buffered under, and the attach
  // would 404 its way through all 60 attempts into a poll that shows nothing.
  const { sandbox, attaches } = harness()
  sandbox.messagesList[0].assistant_message_id = 'srv-9'
  await sandbox.followResumedRun('pend-7')
  assert.deepEqual(attaches, [{ messageId: 'srv-9', pendingId: 'pend-7' }])
})

test('giving up on the stream falls back to the poll, which reloads only once settled', async () => {
  const { sandbox, attaches, polls, timers, resets } = harness({ __attachFails: true })
  await sandbox.followResumedRun('pend-7')
  assert.equal(attaches.length, 1)
  assert.equal(timers.length, 1, 'the give-up arms exactly one fallback poll')
  sandbox.__list = [{ id: 'a-1', is_completed: false }]
  await timers[0].fn()
  assert.equal(polls.length, 1)
  assert.deepEqual(resets, [], 'a still-running message must not end the follow')
  sandbox.__list = [{ id: 'a-1', is_completed: true }]
  await timers[timers.length - 1].fn()
  assert.deepEqual(resets, ['sess-1'])
})

test('a vanished assistant row ends the fallback poll with a reload', async () => {
  // The opposite of the confirm leg, and deliberate: the resumed row is history,
  // so its absence means the row was changed elsewhere and re-polling it is idle.
  const { sandbox, timers, resets } = harness({ __attachFails: true, __list: [{ id: 'other' }] })
  sandbox.pollResumedRun('a-1')
  await timers[0].fn()
  assert.deepEqual(resets, ['sess-1'])
})

test('a message with no such prompt card is left alone', async () => {
  const { sandbox, attaches, prepared, resets } = harness()
  await sandbox.followResumedRun('pend-OTHER')
  assert.deepEqual(attaches, [])
  assert.deepEqual(prepared, [])
  assert.deepEqual(resets, [])
  assert.equal(sandbox.isReplying.value, false)
  assert.equal(sandbox.loading.value, false)
})

test('a stale isReplying from a parked run does not refuse the resume', async () => {
  // The 2026-11-09 live failure. A run that parks can close its stream without a
  // terminal event, which leaves isReplying true; gating the attach on it made
  // the embed page swallow the resume in total silence (zero requests, and the
  // fallback poll sits behind the same guard) while the server's replay buffer
  // already had agent_query(resume_pending_id) as its first event. The main
  // plane's resumeSuspendedRun does not look at isReplying either.
  const { sandbox, attaches, prepared } = harness({ isReplying: { value: true } })
  await sandbox.followResumedRun('pend-7')
  assert.deepEqual(attaches, [{ messageId: 'a-1', pendingId: 'pend-7' }])
  assert.deepEqual(prepared, [{ id: 'a-1', pendingId: 'pend-7' }])
  assert.equal(sandbox.isReplying.value, true)
})

test('a refusal says why instead of returning in silence', async () => {
  const { sandbox, attaches, warns } = harness()
  await sandbox.followResumedRun('pend-OTHER')
  assert.deepEqual(attaches, [])
  assert.equal(warns.length, 1, 'a silent no-op is what made this a 40-minute log dig')
  assert.match(warns[0][1].pendingId, /^pend-OTHER$/)
  assert.equal(warns[0][1].hasMessage, false)
  assert.equal(warns[0][1].hasMessageId, false)
})

test('switching sessions mid-poll stops following the old run', async () => {
  const { sandbox, timers, polls, resets } = harness({ __attachFails: true })
  sandbox.pollResumedRun('a-1')
  sandbox.options.sessionId.value = 'sess-OTHER'
  await timers[0].fn()
  assert.equal(polls.length, 0, 'a switched-away tab must not keep reading the old session')
  assert.deepEqual(resets, [])
})
