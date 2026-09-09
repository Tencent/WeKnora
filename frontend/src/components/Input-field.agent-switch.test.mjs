import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'
import { chatSubmitShortcut } from '../utils/chatSubmitShortcut.ts'

const inputField = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const settingsStore = readFileSync(new URL('../stores/settings.ts', import.meta.url), 'utf8')

test('composer shortcuts queue Enter, inject drafts, and promote the first queued message when empty', () => {
  const code = inputField.slice(inputField.indexOf('const steerShortcutLabel ='), inputField.indexOf('const onPaste ='))
  const sends = [], promotes = []
  const query = ref('draft')
  const composing = ref(false)
  const props = reactive({ isReplying: true, canSteer: true, queuedSteers: [
    { steer_id: 'pending', delivery: 'after', pending: true },
    { steer_id: 'first', delivery: 'after' }, { steer_id: 'second', delivery: 'after' },
  ] })
  const { onKeydown } = vm.runInNewContext(ts.transpile(`${code}\n({ onKeydown })`), {
    navigator: { platform: 'MacIntel' }, computed, props, query, chatSubmitShortcut,
    isComposing: composing, showMention: ref(false),
    createSession: (...args) => sends.push(args), emit: (...args) => promotes.push(args),
  })
  const enter = { key: 'Enter', keyCode: 13, preventDefault() {}, shiftKey: false, ctrlKey: false, altKey: false }
  onKeydown(query.value, { e: enter })
  onKeydown(query.value, { e: { ...enter, altKey: true } })
  onKeydown(query.value, { e: { ...enter, metaKey: true } })
  assert.deepEqual(sends, [['draft', 'after'], ['draft', 'inject'], ['draft', 'inject']])
  query.value = ''
  onKeydown('', { e: { ...enter, metaKey: true } })
  assert.deepEqual(promotes, [['promote-steer', 'first']])
  composing.value = true
  onKeydown('', { e: { ...enter, altKey: true } })
  assert.equal(promotes.length, 1, 'IME confirmation must not promote a message')
})

test('selecting an agent leaves web search off until the user enables it', () => {
  const selectAgentStart = settingsStore.indexOf('selectAgent(agentId: string')
  const getSelectedAgentStart = settingsStore.indexOf('getSelectedAgentId()', selectAgentStart)
  const selectAgentAction = settingsStore.slice(selectAgentStart, getSelectedAgentStart)

  assert.notEqual(selectAgentStart, -1)
  assert.notEqual(getSelectedAgentStart, -1)
  assert.match(selectAgentAction, /this\.settings\.webSearchEnabled = false/)

  const handleSelectAgentStart = inputField.indexOf('const handleSelectAgent = async')
  const handleSelectAgentEnd = inputField.indexOf('const clearvalue', handleSelectAgentStart)
  const handleSelectAgent = inputField.slice(handleSelectAgentStart, handleSelectAgentEnd)

  assert.notEqual(handleSelectAgentStart, -1)
  assert.notEqual(handleSelectAgentEnd, -1)
  assert.match(handleSelectAgent, /settingsStore\.selectAgent\(agent\.id, sourceTenantId\)/)
  assert.doesNotMatch(handleSelectAgent, /agentWebSearch/)
  assert.doesNotMatch(handleSelectAgent, /settingsStore\.toggleWebSearch/)
})

test('shared-agent web search button waits for source readiness metadata', () => {
  const showWebSearchStart = inputField.indexOf('const showWebSearchButton = computed')
  const showWebSearchEnd = inputField.indexOf('const showImageUploadButton', showWebSearchStart)
  const showWebSearchButton = inputField.slice(showWebSearchStart, showWebSearchEnd)

  assert.notEqual(showWebSearchStart, -1)
  assert.notEqual(showWebSearchEnd, -1)
  assert.match(showWebSearchButton, /isWebSearchReadinessKnown/)
  assert.match(showWebSearchButton, /selectedSharedAgent\.value\?\.web_search_ready/)
})

test('one composer action switches between stop and send', () => {
  const controlsStart = inputField.indexOf('class="control-right"')
  const controls = inputField.slice(controlsStart, controlsStart + 2200)
  assert.notEqual(controlsStart, -1)
  assert.doesNotMatch(inputField, /steer-delivery-toggle/)
  assert.doesNotMatch(inputField, /setSteerDelivery/)
  assert.match(inputField, /emit\('steer-msg', val\.trim\(\), steerMentions, delivery\)/)
  assert.match(controls, /v-if="isReplying && \(!canSteer \|\| !query\.trim\(\)\)"/)
  assert.match(controls, /handleStop/)
  assert.match(controls, /v-else[\s\S]*createSession\(query\)[\s\S]*send-btn/)
  assert.match(controls, /<t-icon name="arrow-up"/)
  assert.doesNotMatch(controls, /steer-inject-btn|<t-icon[^>]*'time'/)
})

// Only agent turns have a round boundary to take a mid-run message at, and a
// teardown that hands leftovers to a follow-up run. Queueing on a quick-answer
// turn would park the message until it expired, so the composer keeps its old
// stop-only behaviour there.
test('mid-run send is limited to agent sessions', () => {
  const chatView = readFileSync(
    new URL('../views/chat/index.vue', import.meta.url), 'utf8')
  assert.match(chatView, /:canSteer="isAgentStreamSession\(\)"/)

  const fnStart = inputField.indexOf('const createSession = async')
  const fn = inputField.slice(fnStart, fnStart + 1600)
  assert.notEqual(fnStart, -1)
  assert.match(fn, /if \(!props\.canSteer\)/)
  // The guard must come before anything queues the message.
  assert.ok(
    fn.indexOf('props.canSteer') < fn.indexOf("emit('steer-msg'"),
    'canSteer must be checked before emitting steer-msg',
  )
})

test('queued follow-ups sit in one block with send-now and remove', () => {
  assert.match(inputField, /class="steer-queue"/)
  assert.match(inputField, /queuedSteers/)
  assert.match(inputField, /emit\('promote-steer', item\.steer_id\)/)
  assert.match(inputField, /emit\('remove-steer', item\.steer_id\)/)
  assert.match(inputField, /class="steer-queue-action steer-queue-remove"/)
  assert.match(inputField, /\$t\('input\.steerQueueSendNow'\)/)
  assert.match(inputField, /:disabled="item\.promoting \|\| item\.pending"/)
  assert.match(inputField, /\.steer-queue-item \+ \.steer-queue-item/)
})

test('mid-run send refuses attachments that have already uploaded', () => {
  const fnStart = inputField.indexOf('const createSession = async')
  const fn = inputField.slice(fnStart, fnStart + 2200)
  assert.notEqual(fnStart, -1)
  assert.match(fn, /steerHasAttachments/)
  assert.match(fn, /uploadedImages\.value\.length/)
})

test('stop waits for the API before confirming discard', () => {
  const fnStart = inputField.indexOf('const handleStop = async')
  const fnEnd = inputField.indexOf('onBeforeRouteUpdate', fnStart)
  assert.notEqual(fnStart, -1)
  assert.notEqual(fnEnd, -1)
  const fn = inputField.slice(fnStart, fnEnd)
  assert.match(fn, /emit\('stop-generation'\)/)
  assert.match(fn, /emit\('stop-confirmed'\)/)
  assert.match(fn, /emit\('stop-failed'\)/)
  assert.ok(
    fn.indexOf("emit('stop-generation')") < fn.indexOf('await stopSession'),
    'UI stop must fire before the stop API',
  )
  assert.ok(
    fn.indexOf('await stopSession') < fn.indexOf("emit('stop-confirmed')"),
    'queue discard must wait for stop API success',
  )
})
