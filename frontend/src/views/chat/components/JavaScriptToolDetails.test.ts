import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'

;(globalThis as any).localStorage = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
  clear: () => {},
  key: () => null,
  length: 0,
}
;(globalThis as any).window = { __RUNTIME_CONFIG__: {} }

let server: ViteDevServer
let Details: Component

before(async () => {
  server = await createServer({
    configFile: false,
    optimizeDeps: { noDiscovery: true, entries: [] },
    plugins: [
      {
        name: 'stub-tdesign',
        enforce: 'pre',
        resolveId(id) {
          if (id === 'tdesign-vue-next') return '\0tdesign'
        },
        load(id) {
          if (id === '\0tdesign') return 'export const MessagePlugin = { success() {}, error() {} }; export default {}'
        },
      },
      vue(),
    ],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true },
    ssr: { noExternal: ['tdesign-vue-next'] },
    appType: 'custom',
  })
  Details = (await server.ssrLoadModule('/src/views/chat/components/JavaScriptToolDetails.vue')).default
})
after(async () => { await server?.close() })

async function render(event: Record<string, unknown>) {
  const app = createSSRApp({ render: () => h(Details, { event }) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  return renderToString(app)
}

const envelope = (result: unknown, calls: unknown[], logs: string[] = [], error?: string) => JSON.stringify({ result, calls, logs, ...(error ? { error } : {}) })

test('renders the script, nested calls, result and logs', async () => {
  const html = await render({
    tool_name: 'run_javascript',
    pending: false,
    success: true,
    duration_ms: 123,
    arguments: {
      code: 'const r = await tools.search_knowledge({query:"x"});\nreturn r.data;',
      timeout_ms: 30000,
    },
    output: envelope(
      { items: [1, 2] },
      [
        { tool: 'search_knowledge', ok: true, ms: 10, chars: 20 },
        { tool: 'read_document', ok: false, ms: 5, chars: 0, error: 'not found' },
      ],
      ['hello from console'],
    ),
  })

  assert.match(html, /Script/)
  assert.match(html, /search_knowledge/)
  assert.match(html, /read_document/)
  assert.match(html, /2 calls/)
  assert.match(html, /1 failed/)
  assert.match(html, /hello from console/)
  assert.match(html, /&quot;items&quot;/)
  assert.match(html, /not found/)
  assert.match(html, /const r = await tools\.search_knowledge/)
})

test('shows the running state and script before a result arrives', async () => {
  const html = await render({
    tool_name: 'run_javascript',
    pending: true,
    arguments: { code: 'return await tools.search_knowledge({query:"x"});' },
    js_progress: [
      { nested_call_id: 'outer-js-1', tool_name: 'read_document', phase: 'start', index: 1 },
    ],
  })

  assert.match(html, /Running/)
  assert.match(html, /1 lines/)
  assert.match(html, /search_knowledge/)
  assert.match(html, /read_document/)
  assert.match(html, /js-live/)
})

test('falls back to raw output when the envelope is malformed', async () => {
  const html = await render({
    tool_name: 'run_javascript',
    pending: false,
    success: false,
    error: 'script stopped',
    arguments: { code: 'return 1;' },
    output: 'not-json-output',
  })

  assert.match(html, /script stopped/)
  assert.match(html, /Raw output/)
  assert.match(html, /not-json-output/)
})
