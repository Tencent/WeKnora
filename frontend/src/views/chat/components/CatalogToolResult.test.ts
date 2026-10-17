import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'

const REAL_COMPONENTS = [
  'ToolResultRenderer.vue',
  'OperationsResult.vue',
  'WeKnoraDocResult.vue',
  'CatalogValue.vue',
  'DocMarkdown.vue',
]

let server: ViteDevServer
let Renderer: Component
before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [
      {
        name: 'isolate-catalog-renderer',
        enforce: 'pre',
        load(id) {
          if (id.endsWith('.vue') && !REAL_COMPONENTS.some((name) => id.endsWith(`/${name}`))) {
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
  return renderToString(app)
}

test('find_operations renders rows instead of raw JSON', async () => {
  const html = await render({
    displayType: 'find_operations',
    success: true,
    output: JSON.stringify({
      operations: [{
        id: 'knowledge.get', risk: 'read', summary: 'Read a knowledge base',
        description: 'Returns <script>alert(1)</script>',
        params: [{ name: 'knowledge_base_id', type: 'string', required: true, description: 'Knowledge base' }],
      }],
      unavailable: [{ id: 'knowledge.delete', risk: 'destructive', summary: 'Delete', reason: 'read-only' }],
      hint: 'Call find_operations again',
    }),
  })
  assert.match(html, /knowledge\.get/)
  assert.match(html, /Read a knowledge base/)
  assert.match(html, /Read</)
  assert.match(html, /knowledge_base_id/)
  assert.match(html, /Required/)
  assert.match(html, /knowledge\.delete/)
  assert.match(html, /Destructive/)
  assert.match(html, /&lt;script&gt;/)
  assert.doesNotMatch(html, /fallback-output|hint|Call find_operations|<script>/)
})

test('run_operation renders projected fields and keeps failures out of the raw dump', async () => {
  const html = await render({
    displayType: 'run_operation',
    success: true,
    output: JSON.stringify({
      operation: 'knowledge.list',
      result: { data: [{ title: '员工手册', parse_status: 'failed' }], truncated: true },
    }),
  })
  assert.match(html, /knowledge\.list/)
  assert.match(html, /员工手册/)
  assert.match(html, /parse_status/)
  assert.match(html, /failed/)
  assert.match(html, /Result truncated/)
  assert.doesNotMatch(html, /fallback-output/)

  const error = await render({ displayType: 'run_operation', success: false, output: 'unknown operation "nope"' })
  assert.match(error, /role="alert"/)
  assert.match(error, /unknown operation/)
  assert.doesNotMatch(error, /fallback-output/)
})

test('product docs render pages, sections, and markdown instead of raw JSON', async () => {
  const search = await render({
    displayType: 'search_weknora_docs',
    success: true,
    output: JSON.stringify({
      results: [{
        path: '03-features/14-wiki', anchor: 'setup', section: '安装',
        url: 'javascript:alert(1)', snippet: '<script>alert(1)</script>',
      }],
    }),
  })
  assert.match(search, /安装/)
  assert.match(search, /03-features\/14-wiki#setup/)
  assert.match(search, /&lt;script&gt;/)
  assert.doesNotMatch(search, /javascript:|fallback-output|<script>/)

  const read = await render({
    displayType: 'read_weknora_doc',
    success: true,
    output: JSON.stringify({
      title: 'Wiki',
      path: '03-features/14-wiki',
      section: '安装',
      url: 'https://docs.example/wiki#setup',
      content: '# 安装\n\n先准备 [环境](https://docs.example/env)。',
      outline: [{ anchor: 'setup', heading: '安装', level: 2 }],
      truncated: true,
    }),
  })
  assert.match(read, /<h1 class="doc-h doc-h-1"/)
  assert.match(read, /安装/)
  assert.match(read, /href="https:\/\/docs\.example\/env"/)
  assert.match(read, /href="https:\/\/docs\.example\/wiki#setup"/)
  assert.match(read, /Open in docs/)
  assert.match(read, /The page continues/)
  assert.doesNotMatch(read, /fallback-output|"content"/)
})
