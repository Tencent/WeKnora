import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createSSRApp, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'

let server: ViteDevServer
let useStream: typeof import('./streame').useStream
let useEmbedChatSession: typeof import('../../composables/useEmbedChatSession').useEmbedChatSession
let transport: { requests: Array<{ url: string; options: { body: string } }> }
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => key === 'weknora_token' ? 'test-token' : null,
  } })
  const mocks: Record<string, string> = {
    '@microsoft/fetch-event-source': `export const requests = []; export async function fetchEventSource(url, options) { requests.push({url, options}); }`,
    '@/utils/index': `export const generateRandomString = () => 'test-request';`,
    '@/i18n': `export default { global: { t: key => key, locale: { value: 'zh-CN' } } };`,
    '@/api/embed': `export const getEmbedMessageList = async () => ({ data: [] }); export const postEmbedMessageSent = () => {}; export const postEmbedMessageReceived = () => {}; export const relayEmbedWebhookEvent = () => {}; export const stopEmbedSession = async () => {};`,
    '@/utils/embedToast': `export const embedToast = () => {};`,
    '@/composables/useChatStreamHandler': `export const useChatStreamHandler = () => ({ prepareForNewOutgoingMessage() {}, processStreamChunk() {}, markInFlightAssistantStopped() {} });`,
    '@/composables/useStickyBottomOnResize': `export const useStickyBottomOnResize = () => {};`,
    'vue-i18n': `export const useI18n = () => ({ t: key => key });`,
  }
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'stream-request-test', enforce: 'pre',
      resolveId(id) { if (id.startsWith('\0mock:')) return id },
      load(id) { if (id.startsWith('\0mock:')) return mocks[id.slice(6)] },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: [
      ...Object.keys(mocks).map(find => ({ find, replacement: '\0mock:' + find })),
      { find: '@', replacement: fileURLToPath(new URL('../../', import.meta.url)) },
    ] },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  ;({ useStream } = await server.ssrLoadModule('/src/api/chat/streame.ts'))
  ;({ useEmbedChatSession } = await server.ssrLoadModule('/src/composables/useEmbedChatSession.ts'))
  transport = await server.ssrLoadModule('\0mock:@microsoft/fetch-event-source') as typeof transport
})

for (const agentId of ['agent-1', 'builtin-quick-answer']) {
  test(`embed ${agentId} sends the suggested question separately from host context`, async () => {
    let session!: ReturnType<typeof useEmbedChatSession>
    const hostContext = ref<Record<string, unknown>>({ userId: 123, page: '/refunds' })
    await renderToString(createSSRApp({ setup() {
      session = useEmbedChatSession({
        sessionId: ref('session-1'), sessionSig: ref('sig'), visitorId: ref('visitor'),
        channelId: 'channel-1', token: 'embed-token', agentId, kbIds: [], hostContext,
      })
      return () => null
    } }))
    try {
      session.setSuggestionAttribution('set-1', 'question-1')
      await session.sendMsg('What is the refund policy?')
      const body = JSON.parse(transport.requests.at(-1)!.options.body)
      assert.equal(body.query, 'What is the refund policy?')
      assert.deepEqual(body.host_context, { userId: 123, page: '/refunds' })
      assert.deepEqual(body.suggestion_attribution, { suggestion_set_id: 'set-1', question_id: 'question-1' })
      assert.equal(body.channel, 'embed')

      hostContext.value = { page: '/orders' }
      await session.sendMsg('An ordinary question')
      const nextBody = JSON.parse(transport.requests.at(-1)!.options.body)
      assert.equal(nextBody.query, 'An ordinary question')
      assert.deepEqual(nextBody.host_context, { page: '/orders' })
      assert.equal(Object.hasOwn(nextBody, 'suggestion_attribution'), false)
    } finally { session.handleStopGeneration() }
  })
}
after(async () => {
  await server?.close()
  if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})

for (const effort of [undefined, '', 'off', 'auto', 'high', 'max']) {
  test(`SSE body carries only explicit reasoning overrides: ${effort}`, async () => {
    let stream!: ReturnType<typeof useStream>
    await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
    try {
      await stream.startStream({
        session_id: 'session', query: 'question', method: 'POST', url: '/api/v1/agent-chat',
        reasoning_effort: effort,
      })
      assert.equal(stream.error.value, null)
      const body = JSON.parse(transport.requests.at(-1)!.options.body)
      assert.equal(body.reasoning_effort, effort || undefined)
      assert.equal(Object.hasOwn(body, 'reasoning_effort'), !!effort)
    } finally { stream.stopStream() }
  })
}

for (const selected of [true, false, undefined]) {
  test(`SSE HTTP body preserves browser selection ${selected} alongside other tools`, async () => {
    let stream!: ReturnType<typeof useStream>
    await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
    try {
      await stream.startStream({
        session_id: 'new-session', query: '查一下腾讯股价', method: 'POST', url: '/api/v1/agent-chat',
        agent_enabled: true, local_browser_enabled: selected, web_search_enabled: true,
        mcp_service_ids: ['mcp-1'], skill_names: ['report'],
      })
      assert.equal(stream.error.value, null)
      const request = transport.requests.at(-1)!
      assert.equal(request.url, '/api/v1/agent-chat/new-session')
      const body = JSON.parse(request.options.body)
      assert.equal(body.local_browser_enabled, selected)
      assert.equal(Object.hasOwn(body, 'local_browser_enabled'), selected !== undefined)
      assert.equal(body.web_search_enabled, true)
      assert.deepEqual(body.mcp_service_ids, ['mcp-1'])
      assert.deepEqual(body.skill_names, ['report'])
      assert.equal(stream.lastStreamRequest.value?.body?.local_browser_enabled, selected)
    } finally { stream.stopStream() }
  })
}
