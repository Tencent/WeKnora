import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'
import zh from '../../../i18n/locales/zh-CN'

let server: ViteDevServer
let Icon: Component

before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true }, appType: 'custom',
  })
  Icon = (await server.ssrLoadModule('/src/views/knowledge/settings/DataSourceTypeIcon.vue')).default
})
after(async () => { await server?.close() })

async function render(type: string, locale: 'zh-CN' | 'en-US') {
  const app = createSSRApp({ render: () => h(Icon, { type }) })
  app.use(createI18n({
    legacy: false,
    locale,
    fallbackLocale: locale,
    messages: locale === 'zh-CN' ? { 'zh-CN': zh } : { 'en-US': en },
  }))
  return renderToString(app)
}

// The icon carries the same localized name the card subtitle and the type picker show,
// so screen readers and copied page text never see the internal connector identifier.
test('a connected source names its icon with the localized display name, not the raw type', async () => {
  const html = await render('dingtalk', 'zh-CN')
  assert.ok(html.includes('ds-type-icon__img'), 'the icon image must render, otherwise this asserts nothing')
  assert.ok(html.includes('alt="钉钉文档"'), `expected the zh-CN display name, got: ${html}`)
  assert.ok(!html.includes('alt="dingtalk"'), `the raw type leaked into alt: ${html}`)
})

test('the icon name follows the active locale', async () => {
  const html = await render('dingtalk', 'en-US')
  assert.ok(html.includes('ds-type-icon__img'), 'the icon image must render, otherwise this asserts nothing')
  assert.ok(html.includes('alt="DingTalk Docs"'), `expected the en-US display name, got: ${html}`)
  assert.ok(!html.includes('alt="dingtalk"'), `the raw type leaked into alt: ${html}`)
})

test('every icon-backed connector uses its localized name', async () => {
  const icons = await server.ssrLoadModule('/src/views/knowledge/settings/datasourceIcons.ts')
  const types = Object.keys(icons.datasourceIconMap)
  assert.ok(types.length > 0, 'the icon map must not be empty')
  for (const locale of ['zh-CN', 'en-US'] as const) {
    const names: Record<string, string> = (locale === 'zh-CN' ? zh : en).datasource.connector
    for (const type of types) {
      const html = await render(type, locale)
      assert.ok(
        html.includes(`alt="${names[type]}"`),
        `${locale} ${type}: expected alt="${names[type]}", got: ${html}`,
      )
    }
  }
})

// A type can gain an icon before its `datasource.connector.*` message lands, and a data
// source created through the API can carry a type the UI never listed. vue-i18n echoes
// the key back for a missing message, so a plain `t(key) || type` would put the
// untranslated "datasource.connector.github" in the alt text.
test('an icon-backed type without a translation falls back to the bare type', async () => {
  const icons = await server.ssrLoadModule('/src/views/knowledge/settings/datasourceIcons.ts')
  const zhNames: Record<string, string> = zh.datasource.connector
  assert.ok(!zhNames.github, 'github must stay without a translation for this test to mean anything')
  icons.datasourceIconMap.github = 'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg"/>'
  try {
    const html = await render('github', 'zh-CN')
    assert.ok(html.includes('ds-type-icon__img'), 'the icon image must render, otherwise this asserts nothing')
    assert.ok(html.includes('alt="github"'), `expected a fallback to the raw type, got: ${html}`)
    assert.ok(!html.includes('datasource.connector.github'), `an untranslated key leaked into alt: ${html}`)
  } finally {
    delete icons.datasourceIconMap.github
  }
})
