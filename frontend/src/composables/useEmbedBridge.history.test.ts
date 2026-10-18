import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../test-utils/vueFixture'
import type { useEmbedBridge } from './useEmbedBridge'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function setup(t: TestContext) {
  const f = fixture(t)
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
  Object.assign(f.window, { localStorage: storage, parent: f.window })
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
    'vue-router': { useRoute: () => ({ query: { token: 'ems_test' } }) },
    '@/utils/embedSessionHistory': history,
    '@/i18n/embed': { readEmbedLocaleFromUrl: () => '', syncEmbedLocaleFromUrl() {}, applyEmbedLocale() {} },
    '@/api/embed': {
      embedChatSessionStorageKey: (id: string) => `current:${id}`,
      createEmbedSession: () => create(), exchangeEmbedSession() { throw new Error('must not exchange a session token') },
      getEmbedConfig: async () => ({ success: true, data: { agent_id: 'agent' } }),
      getEmbedMessageList: (...args: any[]) => { loads.push(args); return load(...args) },
      getOrCreateEmbedVisitorId: () => 'visitor', isEmbedSessionToken: () => true,
      onEmbedHostContext: () => () => {}, onEmbedHostLocale: () => () => {}, onEmbedHostToken: () => () => {},
      parseEmbedTokenFromLocation: () => '', postEmbedBootstrapRequest() {}, postEmbedReady() {},
    },
  })
  let bridge!: ReturnType<typeof useEmbedBridge>
  const channel = vue.ref('channel')
  const mounted = f.mount(vue.defineComponent({ setup() { bridge = exports.useEmbedBridge(channel); return {} } }))
  return { bridge: () => bridge, storage, values, loads, channel, close: mounted.close,
    create: (fn: typeof create) => { create = fn }, load: (fn: typeof load) => { load = fn } }
}

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
