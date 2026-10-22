// A queued follow-up attaches via continue-stream while the previous POST may
// still be open (it waits up to 3s for the session title). An older stream
// closing or failing must not cut off the stream that replaced it.
import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

type Call = {
  url: string
  options: { signal: AbortSignal, onmessage: (ev: { data: string }) => void, onclose: () => void }
  resolve: () => void
  reject: (err: unknown) => void
}

let server: ViteDevServer
let useStream: typeof import('./streame').useStream
let transport: { calls: Call[] }
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => key === 'weknora_token' ? 'test-token' : null,
  } })
  // Each request stays pending until the test closes or fails it.
  const mocks: Record<string, string> = {
    '@microsoft/fetch-event-source': `export const calls = [];
      export function fetchEventSource(url, options) {
        return new Promise((resolve, reject) => { calls.push({ url, options, resolve, reject }) })
      }`,
    '@/utils/index': `export const generateRandomString = () => 'test-request';`,
    '@/i18n': `export default { global: { t: key => key, locale: { value: 'zh-CN' } } };`,
  }
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'stream-overlap-test', enforce: 'pre',
      resolveId(id) { if (id.startsWith('\0mock:')) return id },
      load(id) { if (id.startsWith('\0mock:')) return mocks[id.slice(6)] },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: [
      ...Object.keys(mocks).map(find => ({ find, replacement: '\0mock:' + find })),
      { find: '@', replacement: fileURLToPath(new URL('../../', import.meta.url)) },
    ] },
    // No file watching needed in tests (also avoids ENOSPC on hosts with few inotify watches).
    server: { middlewareMode: true, hmr: false, watch: null }, appType: 'custom',
  })
  ;({ useStream } = await server.ssrLoadModule('/src/api/chat/streame.ts'))
  transport = await server.ssrLoadModule('\0mock:@microsoft/fetch-event-source') as typeof transport
})
after(async () => {
  await server?.close()
  if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})

async function nextCall(count: number): Promise<Call> {
  for (let i = 0; i < 100 && transport.calls.length < count; i++) await new Promise(r => setTimeout(r, 5))
  assert.equal(transport.calls.length, count, 'fetchEventSource was not called')
  return transport.calls[count - 1]
}

async function overlapping() {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const chunks: unknown[] = []
  stream.onChunk(data => chunks.push(data))
  const base = transport.calls.length
  // First question: the POST stays open after its answer, waiting for the title.
  const first = stream.startStream({ session_id: 's', query: 'q1', method: 'POST', url: '/api/v1/agent-chat' })
  const a = await nextCall(base + 1)
  // The queued follow-up attaches while that POST is still open.
  const second = stream.startStream({ session_id: 's', query: 'assistant-2', method: 'GET', url: '/api/v1/sessions/continue-stream' })
  const b = await nextCall(base + 2)
  assert.match(b.url, /continue-stream\/s\?message_id=assistant-2$/)
  return { stream, chunks, a, b, first, second }
}

test('an older stream closing does not cut the newer follow-up stream', async () => {
  const { stream, chunks, a, b, first, second } = await overlapping()
  a.options.onclose()
  a.resolve()
  await first
  assert.equal(b.options.signal.aborted, false, 'old onclose aborted the new stream')
  assert.equal(stream.isStreaming.value, true)
  b.options.onmessage({ data: JSON.stringify({ response_type: 'answer', id: 'r2', content: 'hi' }) })
  assert.equal(chunks.length, 1, 'new stream events were dropped after the old one closed')
  // The current stream closing still stops as before.
  b.options.onclose()
  b.resolve()
  await second
  assert.equal(stream.isStreaming.value, false)
  assert.equal(b.options.signal.aborted, true)
  assert.equal(stream.error.value, null)
})

test('an older stream failing does not report an error or cut the newer stream', async () => {
  const { stream, chunks, a, b, first, second } = await overlapping()
  a.reject(new Error('old connection reset'))
  await first
  assert.equal(stream.error.value, null, 'old stream error leaked into the current one')
  assert.equal(b.options.signal.aborted, false)
  b.options.onmessage({ data: JSON.stringify({ response_type: 'complete', id: 'r2' }) })
  assert.equal(chunks.length, 1)
  b.options.onclose()
  b.resolve()
  await second
})

test('the current stream failing still reports the error and stops', async () => {
  const { stream, a, b, first, second } = await overlapping()
  b.reject(new Error('current connection reset'))
  await second
  assert.match(String(stream.error.value), /current connection reset/)
  assert.equal(stream.isStreaming.value, false)
  assert.equal(b.options.signal.aborted, true)
  a.resolve()
  await first
})

test('a disconnected accepted agent POST resumes by GET without executing the query twice', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'spawn children', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'answer-1' }) })
  post.reject(new Error('server restarted'))
  await new Promise(r => setTimeout(r, 1050))
  const resumed = await nextCall(base + 2)
  assert.match(resumed.url, /\/sessions\/continue-stream\/parent\?message_id=answer-1$/)
  assert.equal((resumed.options as any).method, 'GET')
  assert.equal((resumed.options as any).body, null)
  assert.equal(stream.isStreaming.value, true)
  assert.equal(stream.error.value, null)
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete', id: 'r1' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.isStreaming.value, false)
})

test('stop during reconnect backoff prevents the old parent from attaching again', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'spawn children', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  post.reject(new Error('connection reset'))
  await new Promise(r => setTimeout(r, 20))
  stream.stopStream()
  await run
  await new Promise(r => setTimeout(r, 1050))
  assert.equal(transport.calls.length, base + 1)
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
})

test('EOF without complete attaches to the original message rather than declaring completion', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  try { post.options.onclose(); assert.fail('an incomplete EOF must request recovery') }
  catch (error) { post.reject(error) }
  await new Promise(r => setTimeout(r, 1050))
  const resumed = await nextCall(base + 2)
  assert.equal((resumed.options as any).method, 'GET')
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.error.value, null)
})

test('a new stream supersedes an old reconnect without being stopped or receiving old output', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const first = stream.startStream({ session_id: 'parent', query: 'old', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'old-answer' }) })
  post.reject(new Error('server restarted'))
  await new Promise(r => setTimeout(r, 20))
  const second = stream.startStream({ session_id: 'another-session', query: 'new', method: 'POST', url: '/api/v1/agent-chat' })
  const current = await nextCall(base + 2)
  await first
  assert.equal(transport.calls.length, base + 2)
  assert.equal(current.options.signal.aborted, false)
  assert.equal(stream.isStreaming.value, true)
  assert.equal(stream.error.value, null)
  current.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  current.options.onclose()
  current.resolve()
  await second
})

test('a silent broken proxy connection times out and resumes by GET; heartbeats stay out of chat', async (t) => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const chunks: unknown[] = []
  stream.onChunk(value => chunks.push(value))
  const base = transport.calls.length
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const run = stream.startStream({ session_id: 'parent', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post = transport.calls[base]
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  t.mock.timers.tick(20000)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'heartbeat' }) })
  assert.equal(chunks.length, 1)
  t.mock.timers.tick(20000)
  assert.equal(post.options.signal.aborted, false)
  t.mock.timers.tick(10001)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  assert.equal(post.options.signal.aborted, true)
  t.mock.timers.tick(1000)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  const resumed = transport.calls[base + 1]
  assert.ok(resumed)
  assert.equal((resumed.options as any).method, 'GET')
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
})
