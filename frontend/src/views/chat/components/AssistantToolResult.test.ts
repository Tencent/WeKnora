import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { readFileSync } from 'node:fs'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'
import { getAssistantToolDisplayType } from '../../../utils/assistantToolDisplay'

let server: ViteDevServer
let Renderer: Component
before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [
      {
        name: 'isolate-assistant-renderer', enforce: 'pre',
        load(id) {
          // Keep the actual dispatcher and new result components; unrelated
          // components import browser-only application services.
          if (id.endsWith('.vue') && !['ToolResultRenderer.vue', 'OperationToolResult.vue', 'OperationValue.vue', 'WeKnoraDocResult.vue'].some(name => id.endsWith(`/${name}`))) {
            return '<template><div /></template>'
          }
        },
      }, vue(),
    ],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  Renderer = (await server.ssrLoadModule('/src/views/chat/components/ToolResultRenderer.vue')).default
})
after(async () => { await server?.close() })

async function render(tool: string, payload: unknown, extra: Record<string, unknown> = {}) {
  const props = { displayType: getAssistantToolDisplayType(tool), output: typeof payload === 'string' ? payload : JSON.stringify(payload), ...extra }
  const app = createSSRApp({ render: () => h(Renderer, props) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  const html = await renderToString(app)
  assert.doesNotMatch(html, /fallback-output/)
  return html
}

test('both timeline layouts resolve assistant tools, titles and error-only events', () => {
  const source = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')
  assert.match(source, /const assistantType = getAssistantToolDisplayType\(event\?\.tool_name\)/)
  assert.match(source, /const assistantTitle = getAssistantToolTitle\(t, event\)/)
  assert.equal(source.match(/getAssistantToolDisplayType\(event.tool_name\) \? \(event.output \|\| event.error\)/g)?.length, 2)
})

test('operation discovery renders risk, parameters and unavailable reasons in history and SSE', async () => {
  const payload = { operations: [{ id: 'knowledge.get', summary: 'Read a document', risk: 'read', params: [{ name: 'id', type: 'string', required: true, description: '<script>unsafe</script>' }] }], unavailable: [{ id: 'knowledge.delete', summary: 'Delete a document', risk: 'destructive', reason: 'Needs admin role' }] }
  for (const extra of [{}, { success: true, toolData: { output: JSON.stringify(payload), success: true, error: '' } }]) {
    const html = await render('find_operations', payload, extra)
    for (const text of ['knowledge.get', 'Required', 'Read', 'Destructive', 'Needs admin role', 'Available operations: 1', 'Unavailable operations: 1']) assert.ok(html.includes(text), text)
    assert.match(html, /&lt;script&gt;/)
    assert.doesNotMatch(html, /<script>/)
  }
})

test('operation results preserve structured values, pagination metadata and truncation', async () => {
  const html = await render('run_operation', { operation: 'knowledge.list', result: { data: [{ title: '<img src=x onerror=alert(1)>', count: 0, enabled: false, answers: ['one', 'two'] }], meta: { total: 42, page: 1 }, truncated: true } }, { success: true })
  for (const text of ['knowledge.list', 'Only part', 'total', '42', 'false', '>0<', 'one', 'two', '&lt;img']) assert.ok(html.includes(text), text)
  assert.doesNotMatch(html, /<img/)
  const historical = await render('run_operation', { operation: 'knowledge.delete', result: { data: {} } })
  assert.doesNotMatch(historical, /role="alert"|Completed/)
  assert.match(historical, /No items to display/)
})

test('named operation records use compact disclosure rows without losing failure details', async () => {
  const html = await render('run_operation', { operation: 'knowledge.list', result: { data: [
    { title: 'Guide.pdf', parse_status: 'completed', chunk_count: 48 },
    { name: 'Setup.md', parse_status: 'failed', error: 'Model unavailable' },
    { count: 0, enabled: false },
  ] } }, { success: true })
  assert.match(html, /<details class="value-record"/)
  assert.doesNotMatch(html, /<details[^>]*open/)
  for (const text of ['Guide.pdf', 'Setup.md', 'Completed', 'Failed', 'Model unavailable', '48', '>0<', 'false']) assert.ok(html.includes(text), text)
})

test('empty operation searches, declines, plain errors and truncated historical JSON remain readable', async () => {
  const empty = await render('find_operations', { operations: [], hint: 'Try another query' })
  assert.match(empty, /No items to display/)
  assert.match(empty, /Try another query/)
  for (const tool of ['find_operations', 'run_operation', 'search_weknora_docs', 'read_weknora_doc']) {
    const html = await render(tool, 'The user declined <script>unsafe</script>', { success: false })
    assert.match(html, /role="alert"/)
    assert.match(html, /The user declined &lt;script&gt;/)
    assert.doesNotMatch(html, /Completed|<script>/)
    const malformed = await render(tool, '{"partially saved')
    assert.match(malformed, /partially saved/)
  }
})

test('document search and page lists show links and escaped snippets without unsafe links', async () => {
  const html = await render('search_weknora_docs', { results: [
    { path: '03-features/wiki', section: 'Wiki > Setup', snippet: '<script>unsafe</script>', url: 'https://weknora.example/docs/wiki#setup' },
    { path: 'unsafe', section: 'Unsafe link', url: 'javascript:alert(1)' },
  ] })
  assert.match(html, /Documents: 2/)
  assert.match(html, /href="https:\/\/weknora.example\/docs\/wiki#setup"/)
  assert.match(html, /rel="noopener noreferrer"/)
  assert.match(html, /&lt;script&gt;/)
  assert.doesNotMatch(html, /javascript:|<script>/)
  const pages = await render('search_weknora_docs', { pages: [{ title: 'Wiki guide', path: 'wiki', category: 'features' }] })
  assert.match(pages, /Wiki guide/)
  assert.match(pages, /features/)
  const empty = await render('search_weknora_docs', { results: [], hint: 'Try Chinese terms' })
  assert.match(empty, /No items to display/)
  assert.match(empty, /Try Chinese terms/)
})

test('document reads show outlines and continuation offsets, including error recovery outlines', async () => {
  const payload = { path: 'wiki', title: 'Wiki guide', url: 'https://weknora.example/docs/wiki', content: '', outline: [{ anchor: 'setup', heading: 'Setup', level: 2 }], truncated: true, next_offset: 8000, hint: 'Read the next section' }
  const html = await render('read_weknora_doc', payload)
  for (const text of ['Wiki guide', 'Page outline', 'Setup', '8000', 'Read the next section']) assert.ok(html.includes(text), text)
  assert.match(html, /href="https:\/\/weknora.example\/docs\/wiki#setup"/)
  const failed = await render('read_weknora_doc', { ...payload, error: 'Unknown anchor' }, { success: false })
  assert.match(failed, /role="alert"/)
  assert.match(failed, /Unknown anchor/)
  assert.match(failed, /Page outline/)
  assert.match(failed, /<details[^>]*open/)
})

test('document body rendering keeps source text safe when no browser DOM is available', async (t) => {
  // The shared Markdown sanitizer deliberately escapes HTML during SSR, where
  // DOMPurify has no browser document. Its Markdown parsing has separate tests.
  const log = t.mock.method(console, 'error', () => {})
  try {
    const html = await render('read_weknora_doc', {
      path: 'wiki', title: 'Wiki guide', content: '# Wiki setup\n\n<script>alert(1)</script>\n\nRun the setup command.',
      outline: [{ heading: 'Setup', anchor: 'setup' }],
    })
    assert.match(html, /doc-content/)
    assert.match(html, /Wiki setup/)
    assert.match(html, /Run the setup command/)
    assert.doesNotMatch(html, /<script>/)
    assert.doesNotMatch(html, /<details[^>]*open/)
  } finally { log.mock.restore() }
})
