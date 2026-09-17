import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

const { descriptor } = parse(readFileSync(new URL('./ResultItem.vue', import.meta.url), 'utf8'))

test('result titles and descriptions render untrusted names as text', async () => {
  const app = createSSRApp({
    template: descriptor.template!.content,
    data: () => ({
      index: 0, selected: false, iconName: '', badge: '', badgeVariant: '', score: null, shortcut: null,
      title: '<img src=x onerror=alert(1)>', subtitle: '<svg onload=alert(1)>workspace</svg>',
    }),
    methods: { onHover() {} },
  })
  app.component('TIcon', { template: '<span />' })
  const html = await renderToString(app)
  assert.ok(!html.includes('<img'))
  assert.ok(!html.includes('<svg'))
  assert.ok(html.includes('&lt;img'))
  assert.ok(html.includes('&lt;svg'))
})
