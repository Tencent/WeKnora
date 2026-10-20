import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { parse, compileTemplate } from '@vue/compiler-sfc'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import zh from '../../../i18n/locales/zh-CN'
let server: ViteDevServer
let Card: Component
before(async () => {
  server = await createServer({ configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false }, appType: 'custom' })
  Card = (await server.ssrLoadModule('/src/views/chat/components/CollaborationTasks.vue')).default
  // Compile the integration component as well: both timeline branches render progress.
  const source = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const compiled = compileTemplate({ source: descriptor.template!.content, filename: 'AgentStreamDisplay.vue', id: 'test' })
  assert.deepEqual(compiled.errors, [])
})
after(async () => { await server?.close() })
async function render(props: Record<string, unknown>) {
  const app = createSSRApp({ render: () => h(Card, { toolLabel: () => '搜索网页', ...props }) })
  app.use(createI18n({ legacy: false, locale: 'zh-CN', messages: { 'zh-CN': zh } }))
  return renderToString(app)
}
test('child rows expose a readable task name and current tool without a top summary', async () => {
  const html = await render({ task: { id: 'private-id', name: 'review', displayName: '审核任务',
    sessionId: 's', runId: 'r', status: 'running', progress: { tool_name: 'web_search' } } })
  assert.match(html, /审核任务/)
  assert.match(html, /正在使用工具 · 搜索网页/)
  assert.doesNotMatch(html, /collaboration-summary|private-id|<details/)
})
test('completed child rows hide obsolete running tool progress', async () => {
  const html = await render({ task: { id: 'a', displayName: '检索', sessionId: 's', status: 'succeeded', progress: { tool_name: 'web_search' } } })
  assert.match(html, /已完成/)
  assert.doesNotMatch(html, /正在使用工具/)
})
