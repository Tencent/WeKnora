import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as finalContent from '../utils/finalArtifactContent.ts'
import * as history from '../utils/rag-pipeline-history.ts'
import * as timestamps from '../utils/messageTimestamp.ts'
import * as steering from '../utils/steerStreamFork.ts'
import { useProtectedImageRecovery } from './useProtectedImageRecovery.ts'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages } from '../utils/security.ts'
import { setDefaultProtectedFileAccess } from '../utils/protectedFileAccess.ts'
import type { useChatStreamHandler as StreamHandler } from './useChatStreamHandler.ts'

// Load the entire production SSE handler, not a sliced switch branch. Only
// useI18n needs a setup-context stub; every state transition/helper is real.
const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules: Record<string, unknown> = {
  vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
  '@/utils/finalArtifactContent': finalContent,
  '@/utils/rag-pipeline-history': history,
  '@/utils/messageTimestamp': timestamps,
  '@/utils/steerStreamFork': steering,
}
const exports: { useChatStreamHandler?: typeof StreamHandler } = {}
vm.runInNewContext(compiled, {
  exports, console,
  require: (id: string) => {
    assert.ok(id in modules, `unexpected import ${id}`)
    return modules[id]
  },
})
const useChatStreamHandler = exports.useChatStreamHandler!

function setup(t: TestContext, mode: 'ordinary' | 'quick-timeline' | 'agent') {
  const effects = vue.effectScope()
  const oldWindow = (globalThis as any).window
  const oldFetch = globalThis.fetch
  ;(globalThis as any).window = { location: { origin: 'http://localhost' }, addEventListener() {} }
  clearProtectedFileFailureCache()
  t.after(() => {
    effects.stop()
    globalThis.fetch = oldFetch
    ;(globalThis as any).window = oldWindow
    setDefaultProtectedFileAccess(null)
    clearProtectedFileFailureCache()
  })
  const source = `local://2/exports/${mode}-${encodeURIComponent(t.name)}.png`
  const attrs: Record<string, string> = { src: source }
  const img = {
    dataset: {} as Record<string, string>, style: { display: '' },
    get src() { return attrs.src }, set src(value: string) { attrs.src = value },
    getAttribute: (key: string) => attrs[key] || '',
    setAttribute: (key: string, value: string) => { attrs[key] = value },
    removeAttribute: (key: string) => { delete attrs[key] },
  }
  const root = { querySelectorAll: () => [img] } as unknown as ParentNode
  const message = vue.reactive<Record<string, any>>({
    id: 'assistant', assistant_message_id: 'assistant', request_id: 'request', role: 'assistant',
    content: '', is_completed: false, isAgentMode: mode !== 'ordinary',
  })
  const messagesList = vue.reactive([message])
  const loading = vue.ref(true), isReplying = vue.ref(true)
  const currentAssistantMessageId = vue.ref('assistant'), fullContent = vue.ref('')
  const replies: string[] = [], turns: unknown[] = [], errors: string[] = []
  const handler = useChatStreamHandler({
    messagesList, loading, isReplying, currentAssistantMessageId, fullContent,
    isAgentStreamSession: () => mode === 'agent', scrollToBottom() {},
    onReplyComplete: content => replies.push(content), onTurnComplete: message => turns.push(message),
    onError: error => errors.push(error),
  })
  const access = () => ({ mode: 'message' as const, sessionId: 'session', messageId: String(message.assistant_message_id) })
  const revealed = vue.ref(Number.MAX_SAFE_INTEGER)
  const ready = vue.computed(() => Boolean(message.is_completed) && !message.persistence_error
    && revealed.value >= message.content.length)
  effects.run(() => useProtectedImageRecovery(() => root, access, () => ready.value))
  let persisted = false
  const requests: Array<{ url: string; headers: unknown }> = []
  globalThis.fetch = async (url, init) => {
    requests.push({ url: String(url), headers: init?.headers })
    // Immediate body decoding makes flushing Vue/microtasks deterministic.
    return {
      ok: persisted, status: persisted ? 200 : 403,
      headers: new Headers(), blob: async () => new Blob(['png'], { type: 'image/png' }),
    } as Response
  }
  const answer = `![host image](${source})`
  const send = (response_type: string, content = '', done = false, data: Record<string, unknown> = {}) => {
    handler.processStreamChunk({ id: 'request', response_type, content, done, data })
  }
  return {
    message, loading, isReplying, currentAssistantMessageId, replies, turns, errors, img, requests, answer, send, revealed,
    persist: () => { persisted = true },
    render: () => hydrateProtectedFileImages(root, access()),
    flush: async () => {
      await vue.nextTick()
      await vue.nextTick()
      await new Promise(resolve => setImmediate(resolve))
    },
  }
}

for (const mode of ['ordinary', 'quick-timeline', 'agent'] as const) {
  test(`${mode}: answer.done → persistence → complete retries the image exactly once`, async t => {
    const h = setup(t, mode)
    h.send('answer', h.answer, false, { event_id: 'answer' })
    await h.render() // Actual streaming render before authorization evidence is saved.
    assert.equal(h.requests.length, 1)
    h.send('answer', '', true, { event_id: 'answer', done: true })
    await h.flush() // Arbitrarily slow checkpoint/artifact collection can run here.
    assert.equal(h.message.is_completed, false)
    assert.equal(h.isReplying.value, true)
    assert.equal(h.currentAssistantMessageId.value, 'assistant')
    assert.equal(h.replies.length, 0)
    assert.equal(h.turns.length, 0)
    assert.equal(h.requests.length, 1, 'answer.done must not trigger the final retry')
    h.persist()
    h.send('complete', '', true, { final_content: h.answer, artifacts: [{ file_name: 'chart.png' }] })
    await h.flush()
    assert.match(h.img.src, /^blob:/)
    assert.equal(h.requests.length, 2, 'completion bypasses the earlier 403 cooldown')
    assert.equal(h.message.content, h.answer)
    assert.equal(h.message.is_completed, true)
    assert.equal(h.isReplying.value, false)
    assert.equal(h.replies.length, 1)
    assert.equal(h.turns.length, 1)
    assert.equal(h.message.artifacts[0].file_name, 'chart.png')
  })

  test(`${mode}: persistence failure preserves the streamed answer and timeline`, async t => {
    const h = setup(t, mode)
    h.send('answer', h.answer, false, { event_id: 'answer' })
    h.send('answer', '', true, { event_id: 'answer', done: true })
    const timeline = JSON.stringify(h.message.agentEventStream)
    h.send('error', 'Failed to save assistant message', true, { stage: 'message_persistence' })
    await h.flush()
    assert.equal(h.message.content, h.answer)
    assert.equal(JSON.stringify(h.message.agentEventStream), timeline)
    assert.equal(h.message.persistence_error, 'Failed to save assistant message')
    assert.equal(h.message.is_completed, true)
    assert.equal(h.isReplying.value, false)
    assert.deepEqual(h.errors, ['Failed to save assistant message'])
    assert.equal(h.turns.length, 0, 'unsaved output must not start follow-up requests')
    assert.equal(h.requests.length, 0, 'a persistence error must not trigger a completion retry')
  })
}

test('ordinary completion waits for the buffered typewriter before recovering images', async t => {
  const h = setup(t, 'ordinary')
  h.revealed.value = 0
  h.send('answer', h.answer)
  await h.render()
  h.send('answer', '', true)
  h.persist()
  h.send('complete', '', true, { final_content: h.answer })
  await h.flush()
  assert.equal(h.requests.length, 1)
  h.revealed.value = h.answer.length
  await h.flush()
  assert.equal(h.requests.length, 2)
  assert.match(h.img.src, /^blob:/)
})

test('corrected message IDs reauthorize images without requiring completion', async t => {
  const h = setup(t, 'ordinary')
  h.send('answer', h.answer)
  await h.render()
  h.persist()
  h.message.assistant_message_id = 'corrected'
  await h.flush()
  assert.equal(h.requests.length, 2)
  assert.ok(h.requests[1].url.startsWith('/api/v1/sessions/session/messages/corrected/files?'))
  assert.match(h.img.src, /^blob:/)
})

test('completion retries preserve the embed authentication plane', async t => {
  const h = setup(t, 'ordinary')
  setDefaultProtectedFileAccess({ mode: 'embed', channelId: 'channel', token: 'embed-token' })
  h.send('answer', h.answer)
  await h.render()
  h.send('answer', '', true)
  h.persist()
  h.send('complete', '', true, { final_content: h.answer })
  await h.flush()
  assert.equal(h.requests.length, 2)
  assert.ok(h.requests.every(request => request.url.startsWith('/api/v1/embed/channel/files?')))
  assert.deepEqual(h.requests[1].headers, { Authorization: 'Embed embed-token' })
  assert.match(h.img.src, /^blob:/)
})
