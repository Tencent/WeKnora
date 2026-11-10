import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'

let server: ViteDevServer
let Renderer: Component
before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [
      {
        name: 'isolate-mcp-renderer',
        enforce: 'pre',
        load(id) {
          // Unused result components depend on browser-only app services.
          // Keep the real renderer, MCP component and shared result rows.
          if (id.endsWith('.vue') && !['ToolResultRenderer.vue', 'McpToolResult.vue', 'ResultRow.vue'].some((name) => id.endsWith(`/${name}`))) {
            return '<template><div /></template>'
          }
        },
      },
      vue(),
    ],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true }, appType: 'custom',
  })
  Renderer = (await server.ssrLoadModule('/src/views/chat/components/ToolResultRenderer.vue')).default
})
after(async () => { await server?.close() })

async function render(props: Record<string, unknown>) {
  const app = createSSRApp({ render: () => h(Renderer, props) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  app.component('t-icon', { render: () => h('span') })
  app.component('t-popup', { render: () => h('span') })
  return renderToString(app)
}

test('discovery renders tool rows instead of raw metadata, including old history', async () => {
  const html = await render({ displayType: 'mcp_discovery', success: true, output: JSON.stringify({
    mode: 'list_tools', server_name: 'Svrlog Mcp Server',
    tools: [{ name: 'get_log', description: '<script>alert(1)</script>', tool_ref: 'mcpt_old' }],
    total: 3, has_more: true, notice: 'external',
  }) })
  assert.match(html, /get_log/)
  assert.match(html, /Svrlog Mcp Server/)
  assert.match(html, /Showing 1 of 3/)
  assert.match(html, /More results available/)
  assert.match(html, /&lt;script&gt;/)
  assert.doesNotMatch(html, /fallback-output|mcpt_old|<script>/)
})

test('definition renders parameters and expandable complete schema', async () => {
  const html = await render({ displayType: 'mcp_discovery', output: JSON.stringify({
    name: 'get_log', server_name: 'Svrlog Mcp Server',
    input_schema: { type: 'object', required: ['start_time'], properties: { start_time: { type: 'string' } } },
  }) })
  assert.match(html, /start_time/)
  assert.match(html, /Svrlog Mcp Server/)
  assert.match(html, /Required/)
  assert.match(html, /<details/)
  assert.match(html, /Full parameter definition/)
  assert.doesNotMatch(html, /fallback-output/)
})

test('proxy validation errors and empty service lists use dedicated views', async () => {
  const error = await render({ displayType: 'mcp_call', success: false, output: 'arguments must be an object' })
  assert.match(error, /role="alert"/)
  assert.match(error, /arguments must be an object/)
  assert.doesNotMatch(error, /fallback-output/)
  const empty = await render({ displayType: 'mcp_discovery', output: '{"mode":"list_servers","total":0,"has_more":false}' })
  assert.match(empty, /Showing 0 of 0/)
  assert.doesNotMatch(empty, /fallback-output/)
  const live = await render({
    displayType: 'mcp_discovery',
    success: true,
    output: JSON.stringify({ mode: 'list_tools', tools: [{ name: 'get_log' }], total: 1 }),
    toolData: { tool_name: 'discover_mcp_tools', success: true, output: '{"mode":"list_tools"}', error: '' },
  })
  assert.match(live, /get_log/)
  assert.doesNotMatch(live, /fallback-output/)
})
