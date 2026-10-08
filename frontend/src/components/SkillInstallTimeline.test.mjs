import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('./SkillInstallTimeline.vue', import.meta.url), 'utf8')
const logic = source.slice(source.indexOf('const emit = defineEmits'), source.indexOf('const messages = reactive'))
const compiled = ts.transpileModule(logic, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function harness(overrides = {}) {
  const watches = [], emitted = []
  let id = 0
  const state = {
    props: { configId: 'cfg', skillId: 'skill', messageId: 'run', live: true, canRetry: false },
    ref: value => ({ value }), defineEmits: () => name => emitted.push(name),
    watch: (_source, callback) => watches.push(callback), onUnmounted() {},
    setTimeout: () => 1, clearTimeout() {}, makeSteerClientId: () => `id-${++id}`,
    i18n: { global: { t: key => key } },
    getConfigSkillGuidance: async () => ({ data: { accepting: true, messages: [] } }),
    steerConfigSkill: async () => {}, reinstallConfigSkill: async () => {},
    ...overrides,
  }
  const actions = vm.runInNewContext(`${compiled}\n({ sendGuidance, refreshGuidance, guidance, guidanceText, guidanceError, sendingGuidance })`, state)
  actions.guidance.value.accepting = true
  return { ...actions, state, watches, emitted }
}

test('uncertain sends preserve the draft and retry the same message ID', async () => {
  const calls = []
  const h = harness({ steerConfigSkill: async (...args) => {
    calls.push(args)
    if (calls.length === 1) throw new Error('network disconnected')
  } })
  h.guidanceText.value = 'Install the CLI'
  await h.sendGuidance()
  assert.equal(h.guidanceText.value, 'Install the CLI')
  assert.equal(h.guidanceError.value, 'network disconnected')
  await h.sendGuidance()
  assert.equal(calls[0][2].steer_id, calls[1][2].steer_id)
  assert.equal(calls[1][2].expected_message_id, 'run')
  assert.equal(h.guidanceText.value, '')
  assert.equal(h.guidance.value.messages.length, 1)
})

test('a success needs no extra read to acknowledge and clear the draft', async () => {
  const h = harness({ getConfigSkillGuidance: async () => { throw new Error('read failed') } })
  h.guidanceText.value = 'Verify readiness'
  await h.sendGuidance()
  assert.equal(h.guidanceText.value, '')
  assert.equal(h.guidanceError.value, '')
  assert.equal(h.guidance.value.messages[0].status, 'pending')
})

test('stale polling results cannot enable input for a different run', async () => {
  const request = deferred()
  let calls = 0
  const h = harness({ getConfigSkillGuidance: () => ++calls === 1 ? request.promise : Promise.resolve({ data: { accepting: true, messages: [] } }) })
  const poll = h.refreshGuidance(0)
  h.state.props.messageId = 'new-run'
  h.watches[0]()
  await Promise.resolve()
  request.resolve({ data: { accepting: false, messages: [] } })
  await poll
  assert.equal(h.guidance.value.accepting, true)
})

test('a run switch while sending preserves the draft instead of clearing another run', async () => {
  const request = deferred()
  const h = harness({ steerConfigSkill: () => request.promise })
  h.guidanceText.value = 'Guidance for old run'
  const sending = h.sendGuidance()
  h.state.props.messageId = 'new-run'
  h.watches[0]()
  request.resolve()
  await sending
  assert.equal(h.guidanceText.value, 'Guidance for old run')
  assert.equal(h.sendingGuidance.value, false)
})

test('verification refuses live sends and a finished run reinstalls with instructions', async () => {
  const calls = []
  const h = harness({ reinstallConfigSkill: async (...args) => calls.push(args) })
  h.guidance.value.accepting = false
  h.guidanceText.value = 'Install from the official guide'
  await h.sendGuidance()
  assert.equal(h.guidanceText.value, 'Install from the official guide')
  h.state.props.live = false
  h.state.props.canRetry = true
  await h.sendGuidance()
  assert.equal(calls.length, 1)
  assert.equal(calls[0][2], 'Install from the official guide')
  assert.deepEqual(h.emitted, ['restarted'])
})

test('live output renders before tool completion and ignores a previous run', async () => {
  const progressLogic = source.slice(source.indexOf('const commandOutput ='), source.indexOf('const loading ='))
  const followLogic = source.slice(source.indexOf('async function follow('), source.indexOf('function wait('))
  const js = ts.transpileModule(`${progressLogic}\n${followLogic}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText
  let handlers
  const modelChunks = []
  const h = vm.runInNewContext(`${js}\n({ follow, commandOutput, changeRun: () => { openRun++ } })`, {
    props: { live: true, configId: 'cfg', skillId: 'skill' },
    ref: value => ({ value }), computed: get => ({ get value() { return get() } }),
    watch() {}, onUnmounted() {}, clearInterval() {},
    getApiBaseUrl: () => '', configSkillTranscriptUrl: () => '/transcript',
    localStorage: { getItem: () => null }, AbortController,
    controller: null, openRun: 1, i18n: { global: {} }, generateRandomString: () => 'request',
    fetchEventSource: async (_url, options) => { handlers = options; await options.onopen({ ok: true }) },
    applyPrompt() {}, processStreamChunk: frame => modelChunks.push(frame),
  })
  assert.equal(await h.follow(1), true)
  const frame = data => handlers.onmessage({ data: JSON.stringify({ response_type: 'install_output', data }) })
  const started = new Date('2026-09-10T12:00:00Z').toISOString()
  frame({ command: 'uv pip install -r requirements.lock', started_at: started, output: 'Downloading numpy', done: false })
  assert.equal(h.commandOutput.value.output, 'Downloading numpy')
  assert.equal(modelChunks.length, 0)
  frame({ command: 'uv pip install -r requirements.lock', started_at: started, output: 'Installed', done: true })
  assert.equal(h.commandOutput.value.done, true)
  h.changeRun()
  frame({ command: 'old command', output: 'late output', done: false })
  assert.equal(h.commandOutput.value.output, 'Installed')
})
