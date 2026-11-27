import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../test-utils/vueFixture'
import type { useEmbedBridge } from './useEmbedBridge'
import type { useEmbedChatSession } from './useEmbedChatSession'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function setup(t: TestContext, framed = false) {
  const f = fixture(t)
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
  const parent = framed ? {} : f.window
  Object.assign(f.window, { localStorage: storage, parent })
  const api = f.evaluate(readFileSync(new URL('../api/embed/index.ts', import.meta.url), 'utf8'), {
    '@/utils/request': {}, '@/utils/embedBaseUrl': {},
  })
  const provideToken = (token: string, channel_id = 'channel', origin = 'https://host.example', source = parent) => {
    f.window.dispatchEvent(Object.assign(new Event('message'), { source, origin,
      data: { source: 'weknora-host', type: 'provide_token', token, channel_id } }))
  }
  const history = f.evaluate(readFileSync(new URL('../utils/embedSessionHistory.ts', import.meta.url), 'utf8'), {})
  const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
  history.saveEmbedHistory(scope, [
    { id: 'old', sig: 'old-signature', title: 'Previous chat', updatedAt: 1 },
  ])
  let created = 0
  let create: () => Promise<any> = async () => ({ data: { id: `new-${++created}`, sig: 'new-signature' } })
  let load: (...args: any[]) => Promise<any> = async () => ({ success: true, data: [] })
  const loads: any[][] = []
  const source = readFileSync(new URL('./useEmbedBridge.ts', import.meta.url), 'utf8')
    .replaceAll('import.meta.env.DEV', 'false')
  const exports = f.evaluate(source, {
    vue, 'vue-i18n': { useI18n: () => ({ locale: vue.ref('en-US'), t: (key: string) => key }) },
    'vue-router': { useRoute: () => ({ query: framed ? {} : { token: 'ems_test' } }) },
    '@/utils/embedSessionHistory': history,
    '@/i18n/embed': { readEmbedLocaleFromUrl: () => '', syncEmbedLocaleFromUrl() {}, applyEmbedLocale() {} },
    '@/api/embed': {
      embedChatSessionStorageKey: (id: string) => `current:${id}`,
      createEmbedSession: () => create(),
      exchangeEmbedSession: async (_id: string, token: string) => {
        assert.equal(token, 'publish_static', 'must not exchange a session token')
        return { data: { session_token: 'ems_exchanged' } }
      },
      getEmbedConfig: async () => ({ success: true, data: { agent_id: 'agent' } }),
      getEmbedMessageList: (...args: any[]) => { loads.push(args); return load(...args) },
      getOrCreateEmbedVisitorId: () => 'visitor', isEmbedSessionToken: api.isEmbedSessionToken,
      onEmbedHostContext: () => () => {}, onEmbedHostLocale: () => () => {}, onEmbedHostToken: api.onEmbedHostToken,
      parseEmbedTokenFromLocation: () => '', postEmbedBootstrapRequest() {}, postEmbedReady() {},
    },
  })
  let bridge!: ReturnType<typeof useEmbedBridge>
  const channel = vue.ref('channel')
  const mounted = f.mount(vue.defineComponent({ setup() { bridge = exports.useEmbedBridge(channel); return {} } }))
  return { bridge: () => bridge, storage, values, loads, channel, close: mounted.close, history, window: f.window, provideToken,
    fixture: f,
    create: (fn: typeof create) => { create = fn }, load: (fn: typeof load) => { load = fn } }
}

test('a secure widget can rotate its token without replacing the signed chat', async t => {
  const f = setup(t, true)
  f.provideToken('ems_initial')
  await flush()
  const b = f.bridge()
  assert.equal(b.sessionId.value, 'new-1')
  const streams: Array<Record<string, string>> = []
  const { useEmbedChatSession: useChat } = f.fixture.evaluate(
    readFileSync(new URL('./useEmbedChatSession.ts', import.meta.url), 'utf8'), {
      vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      '@/api/chat/streame': { useStream: () => ({ error: vue.ref(''), onChunk() {}, stopStream() {},
        startStream: async (request: Record<string, string>) => { streams.push(request) } }) },
      '@/api/embed': { getEmbedMessageList: async () => ({ data: [] }), postEmbedMessageSent() {}, relayEmbedWebhookEvent() {} },
      '@/utils/embedToast': {}, '@/utils/embedFile': {}, '@/utils/embedContext': { buildQueryWithHostContext: (q: string) => q },
      '@/utils/historyPagination': { historyHasMoreAfterAttempt: () => false }, '@/utils/suspendedRun': { resumeSuspendedRunKey: Symbol() },
      '@/composables/useChatStreamHandler': { useChatStreamHandler: () => ({ prepareForNewOutgoingMessage() {} }) },
      '@/composables/useStickyBottomOnResize': { useStickyBottomOnResize() {} },
    })
  let chat!: ReturnType<typeof useEmbedChatSession>
  f.fixture.mount(vue.defineComponent({ setup() {
    chat = useChat({ sessionId: b.sessionId, sessionSig: b.sessionSig, visitorId: b.visitorId, token: b.token,
      channelId: 'channel', agentId: 'agent', kbIds: [] })
    return {}
  } }))
  await chat.sendMsg('first')
  f.provideToken('ems_wrong', 'other')
  f.provideToken('ems_missing', '')
  f.provideToken('ems_origin', 'channel', 'https://another.example')
  f.provideToken('ems_frame', 'channel', 'https://host.example', {})
  f.provideToken('publish_token')
  await flush()
  assert.equal(f.loads.length, 0)
  f.provideToken('ems_rotated')
  await flush()
  await chat.sendMsg('next')
  assert.deepEqual(streams.map(r => [r.query, r.embed_token, r.session_id, r.embed_session_sig, r.embed_visitor_id]), [
    ['first', 'ems_initial', 'new-1', 'new-signature', 'visitor'],
    ['next', 'ems_rotated', 'new-1', 'new-signature', 'visitor'],
  ])
  assert.deepEqual(f.loads, [['channel', 'ems_rotated', 'new-1', 1, undefined, 'new-signature']])
  assert.equal(b.token.value, 'ems_rotated')
  assert.equal(b.sessionId.value, 'new-1')
  assert.equal(b.sessionSig.value, 'new-signature')
  assert.equal(b.visitorId.value, 'visitor')
})

test('invalid, out-of-order and unmounted rotations cannot replace a working token', async t => {
  const f = setup(t, true)
  f.provideToken('ems_first')
  await flush()
  const slow = deferred<any>()
  f.load(async (_id, token) => {
    if (token === 'ems_slow') return slow.promise
    if (token === 'ems_invalid') throw { response: { status: 401 } }
    return { success: true, data: [] }
  })
  f.provideToken('ems_slow')
  await flush()
  f.provideToken('ems_new')
  await flush()
  slow.resolve({ success: true, data: [] })
  await flush()
  f.provideToken('ems_invalid')
  await flush()
  assert.equal(f.bridge().token.value, 'ems_new')
  const pending = deferred<any>()
  f.load(() => pending.promise)
  f.provideToken('ems_late')
  await flush()
  f.close()
  pending.resolve({ success: true, data: [] })
  await flush()
  assert.equal(f.bridge().token.value, 'ems_new')
})

test('session token handoffs do not switch a publish-token widget into secure mode', async t => {
  const f = setup(t, true)
  f.provideToken('publish_static')
  await flush()
  f.provideToken('ems_other')
  await flush()
  assert.equal(f.bridge().token.value, 'ems_exchanged')
  assert.equal(f.loads.length, 0)
})

test('rotation can finish during bootstrap and while switching chats', async t => {
  const f = setup(t, true)
  const initial = deferred<any>()
  f.create(() => initial.promise)
  f.provideToken('ems_first')
  f.provideToken('ems_second')
  await flush()
  assert.equal(f.loads.length, 0, 'the first signed handle must be ready before probing')
  initial.resolve({ data: { id: 'current', sig: 'signature' } })
  await flush()
  assert.equal(f.bridge().token.value, 'ems_second')
  const switching = deferred<any>()
  f.load(async (_id, _token, sid) => sid === 'old' ? switching.promise : { success: true, data: [] })
  const switched = f.bridge().switchHistorySession('old')
  f.provideToken('ems_third')
  await flush()
  switching.resolve({ success: true, data: [] })
  assert.equal(await switched, true)
  assert.equal(f.bridge().sessionId.value, 'old')
  assert.equal(f.bridge().token.value, 'ems_third')
})

test('signed history switches only after validation and survives subsequent new chats', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  assert.equal(b.sessionId.value, 'new-1')
  assert.equal(b.sessionHistory.value.length, 1, 'empty new sessions must not enter the history')
  b.setSessionHasMessages(true)
  b.rememberCurrentSession('Current title')
  assert.equal(b.sessionHistory.value.length, 2)
  assert.equal(await b.startNewSession(), true)
  assert.equal(await b.switchHistorySession('old'), true)
  assert.equal(b.sessionId.value, 'old')
  assert.equal(b.sessionSig.value, 'old-signature')
  assert.equal(f.loads.at(-1)?.[5], 'old-signature')
  assert.equal(JSON.parse(f.storage.getItem('current:channel')!).id, 'old')
  assert.equal(b.sessionHistory.value.length, 2)
})

test('temporary failures preserve the current chat and history, while definitive denial prunes only that entry', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  f.load(async () => { throw { response: { status: 503 } } })
  assert.equal(await b.switchHistorySession('old'), false)
  assert.equal(b.sessionId.value, 'new-1')
  assert.equal(b.sessionHistory.value.length, 1)
  f.load(async () => { throw Object.defineProperty({ status: 'denied' }, '$httpStatus', { value: 403 }) })
  assert.equal(await b.switchHistorySession('old'), false)
  assert.equal(b.sessionId.value, 'new-1')
  assert.equal(b.sessionHistory.value.length, 0)
})

test('navigation is serialized and late responses cannot activate a session after channel changes or unmount', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  const pending = deferred<any>()
  f.load(() => pending.promise)
  const switching = b.switchHistorySession('old')
  assert.equal(b.sessionBusy.value, true)
  assert.equal(await b.startNewSession(), false)
  f.channel.value = 'another-channel'
  pending.resolve({ success: true, data: [] })
  assert.equal(await switching, false)
  assert.equal(b.sessionId.value, 'new-1')
  f.channel.value = 'channel'
  const next = deferred<any>()
  f.load(() => next.promise)
  const disposed = b.switchHistorySession('old')
  f.close()
  next.resolve({ success: true, data: [] })
  assert.equal(await disposed, false)
  assert.equal(b.sessionId.value, 'new-1')
})

test('clearing history creates a fresh session, and failure keeps every saved handle', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  f.create(async () => { throw new Error('network') })
  assert.equal(await b.clearSessionHistory(), false)
  assert.equal(b.sessionId.value, 'new-1')
  assert.equal(b.sessionHistory.value.length, 1)
  f.create(async () => ({ data: { id: 'fresh', sig: 'fresh-signature' } }))
  assert.equal(await b.clearSessionHistory(), true)
  assert.equal(b.sessionId.value, 'fresh')
  assert.equal(b.sessionHistory.value.length, 0)
  assert.equal(f.storage.getItem('current:channel'), null)
  b.setSessionHasMessages(true)
  assert.equal(JSON.parse(f.storage.getItem('current:channel')!).id, 'fresh')
})

test('a legacy current handle is validated and added to history only after messages load', async t => {
  const f = setup(t)
  f.storage.setItem('current:channel', JSON.stringify({ id: 'legacy', sig: 'legacy-signature', agentId: 'agent' }))
  await flush()
  const b = f.bridge()
  assert.equal(b.sessionId.value, 'legacy')
  assert.equal(f.loads[0]?.[5], 'legacy-signature')
  assert.equal(b.sessionHistory.value.length, 1)
  b.setSessionHasMessages(true)
  assert.equal(b.sessionHistory.value.length, 2)
  assert.equal(JSON.parse(f.storage.getItem('current:channel')!).visitorId, 'visitor')
})

for (const failure of [{ $httpStatus: 503 }, { $httpStatus: 429 }, { $httpStatus: 401 }, new Error('timeout')]) {
  test(`bootstrap preserves the signed handle on ${JSON.stringify(failure)} and retries without creating a session`, async t => {
    const f = setup(t)
    const stored = { id: 'legacy', sig: 'legacy-signature', agentId: 'agent', visitorId: 'visitor' }
    f.storage.setItem('current:channel', JSON.stringify(stored))
    f.load(async () => { throw failure })
    await flush()
    const b = f.bridge()
    assert.equal(b.sessionId.value, '')
    assert.ok(b.loadError.value)
    assert.equal(JSON.parse(f.storage.getItem('current:channel')!).id, 'legacy')
    f.create(async () => { assert.fail('temporary failures must never create a replacement') })
    f.load(async () => ({ success: true, data: [] }))
    await b.retryBootstrap()
    assert.equal(b.sessionId.value, 'legacy')
    assert.equal(b.loadError.value, '')
  })
}

test('bootstrap replaces a definitively unavailable signed handle', async t => {
  const f = setup(t)
  f.storage.setItem('current:channel', JSON.stringify({ id: 'gone', sig: 'sig', agentId: 'agent' }))
  f.load(async () => { throw { $httpStatus: 404 } })
  await flush()
  assert.equal(f.bridge().sessionId.value, 'new-1')
  assert.equal(f.bridge().loadError.value, '')
})

for (const deliverEvent of [false, true]) {
  test(`another tab clears history without stale handles being resurrected (storage event: ${deliverEvent})`, async t => {
    const f = setup(t)
    await flush()
    const b = f.bridge()
    b.setSessionHasMessages(true)
    b.rememberCurrentSession('Current')
    const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
    f.history.clearEmbedHistory(scope)
    if (deliverEvent) {
      const event = new Event('storage')
      Object.defineProperty(event, 'key', { value: f.history.embedHistoryRevisionKey(scope) })
      f.window.dispatchEvent(event)
      assert.equal(b.sessionHistory.value.length, 0)
    }
    // Covers a suspended tab whose storage event is still queued, and late title events.
    b.rememberCurrentSession('Late title')
    b.setSessionHasMessages(true)
    assert.equal(b.sessionHistory.value.length, 0)
    assert.equal(b.sessionId.value, 'new-1', 'the open chat stays usable')
    assert.equal(f.history.readEmbedHistory(scope).length, 0)
    await b.startNewSession()
    b.setSessionHasMessages(true)
    assert.deepEqual(Array.from(b.sessionHistory.value, entry => entry.id), ['new-2'])
  })
}

test('storage events refresh only the matching scope and stop after unmount', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
  f.history.saveEmbedHistory(scope, [{ id: 'other-tab', sig: 'sig', title: 'Other', updatedAt: 2 }])
  const emit = (key: string) => {
    const event = new Event('storage')
    Object.defineProperty(event, 'key', { value: key })
    f.window.dispatchEvent(event)
  }
  emit('unrelated')
  assert.equal(b.sessionHistory.value[0]?.id, 'old')
  emit(f.history.embedHistoryStorageKey(scope))
  assert.equal(b.sessionHistory.value[0]?.id, 'other-tab')
  f.close()
  f.history.saveEmbedHistory(scope, [])
  emit(f.history.embedHistoryStorageKey(scope))
  assert.equal(b.sessionHistory.value[0]?.id, 'other-tab')
})

for (const deliverEvent of [false, true]) {
  test(`a pending switch cannot restore a handle cleared in another tab (storage event: ${deliverEvent})`, async t => {
    const f = setup(t)
    await flush()
    const b = f.bridge()
    const pending = deferred<any>()
    f.load(() => pending.promise)
    const switching = b.switchHistorySession('old')
    const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
    f.history.clearEmbedHistory(scope)
    if (deliverEvent) {
      const event = new Event('storage')
      Object.defineProperty(event, 'key', { value: f.history.embedHistoryRevisionKey(scope) })
      f.window.dispatchEvent(event)
    }
    pending.resolve({ success: true, data: [] })
    assert.equal(await switching, false)
    assert.equal(b.sessionId.value, 'new-1')
    assert.equal(b.sessionHistory.value.length, 0)
    assert.equal(b.sessionBusy.value, false)
  })
}

test('a clear during bootstrap validation prevents restoring the old signed handle', async t => {
  const f = setup(t)
  f.storage.setItem('current:channel', JSON.stringify({ id: 'legacy', sig: 'sig', agentId: 'agent' }))
  const pending = deferred<any>()
  f.load(() => pending.promise)
  await flush()
  assert.equal(f.loads.length, 1)
  f.history.clearEmbedHistory({ channelId: 'channel', agentId: 'agent', visitorId: 'visitor' })
  pending.resolve({ success: true, data: [] })
  await flush()
  assert.equal(f.bridge().sessionId.value, 'new-1')
  assert.equal(f.bridge().sessionHistory.value.length, 0)
})

test('a late denial cannot write a stale history list after another tab clears it', async t => {
  const f = setup(t)
  await flush()
  const b = f.bridge()
  b.setSessionHasMessages(true)
  const pending = deferred<any>()
  f.load(() => pending.promise)
  const switching = b.switchHistorySession('old')
  const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
  f.history.clearEmbedHistory(scope)
  pending.reject({ $httpStatus: 403 })
  assert.equal(await switching, false)
  assert.equal(f.history.readEmbedHistory(scope).length, 0)
  assert.equal(b.sessionHistory.value.length, 0)
})
