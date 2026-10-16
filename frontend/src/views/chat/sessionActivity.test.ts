import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { effectScope, reactive, ref, watch } from 'vue'
import { createSessionActivityState, type SessionActivity } from '../../stores/sessionActivityState'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const start = source.indexOf('watch([activitySessionId,')
const end = source.indexOf('const historyLoading =', start)
assert.ok(start > 0 && end > start)

function fixture() {
  const entries = reactive<Record<string, SessionActivity>>({})
  const sessionActivity = createSessionActivityState(entries, async () => [])
  const refs = {
    activitySessionId: ref('session-a'), isReplying: ref(false), isStreaming: ref(false),
    isImRecovering: ref(false), currentAssistantMessageId: ref('reply-a'),
  }
  const scope = effectScope()
  scope.run(() => runInNewContext(source.slice(start, end), { ...refs, sessionActivity, watch, props: { embeddedMode: false } }))
  return { ...refs, entries, sessionActivity, close: () => scope.stop() }
}

test('a terminal reply clears the sidebar even while its SSE connection remains open', () => {
  const f = fixture()
  try {
    f.isReplying.value = true
    f.isStreaming.value = true
    assert.ok(f.entries['session-a'])
    f.isReplying.value = false
    f.currentAssistantMessageId.value = ''
    assert.equal(f.entries['session-a'], undefined)
    f.isStreaming.value = false
    f.isStreaming.value = true
    assert.equal(f.entries['session-a'], undefined, 'transport changes cannot restart the turn indicator')
  } finally { f.close() }
})

test('generation remains active during connection setup and IM recovery', () => {
  const f = fixture()
  try {
    f.isReplying.value = true
    assert.ok(f.entries['session-a'], 'waiting for headers still counts as generating')
    f.isReplying.value = false
    f.isImRecovering.value = true
    assert.ok(f.entries['session-a'], 'IM polling counts as active without an SSE connection')
    f.isImRecovering.value = false
    assert.equal(f.entries['session-a'], undefined)
  } finally { f.close() }
})

test('stopping the current conversation does not clear a detached conversation', () => {
  const f = fixture()
  try {
    f.isReplying.value = true
    f.sessionActivity.detach('session-a')
    f.activitySessionId.value = ''
    f.isReplying.value = false
    assert.equal(f.entries['session-a']?.detached, true)
    f.activitySessionId.value = 'session-b'
    f.isReplying.value = true
    f.isStreaming.value = true
    f.isReplying.value = false
    assert.equal(f.entries['session-b'], undefined)
    assert.ok(f.entries['session-a'])
    f.isReplying.value = true
    assert.ok(f.entries['session-b'], 'a new turn starts the indicator again')
  } finally { f.close() }
})
