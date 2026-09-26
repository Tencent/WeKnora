import assert from 'node:assert/strict'
import test from 'node:test'

import {
  cardsModel,
  display,
  kvModel,
  markdownSource,
  safeHref,
  tableModel,
  toolArguments,
  valueAt,
  type ToolViewSpec,
} from './pluginToolView'

const result = {
  total: 2,
  issues: [
    { key: 'ENG-1', summary: 'Login fails', url: 'https://acme.atlassian.net/browse/ENG-1', fields: { status: 'Open' }, labels: ['sso', 'web'] },
    { key: 'ENG-2', summary: 'Evil', url: 'javascript:alert(1)', fields: { status: 'Done' } },
  ],
}

test('tables take their rows from the items path and link only to http(s)', () => {
  const spec: ToolViewSpec = {
    view: 'table',
    items: 'issues',
    columns: [
      { field: 'key', link: 'url' },
      { field: 'summary', title: { default: 'Summary', 'zh-CN': '摘要' } },
      { field: 'fields.status' },
      { field: 'labels' },
    ],
  }
  const t = tableModel(spec, result, 'zh-CN')
  assert.deepEqual(t.headers, ['key', '摘要', 'fields.status', 'labels'])
  assert.deepEqual(t.rows[0], [
    { text: 'ENG-1', href: 'https://acme.atlassian.net/browse/ENG-1' },
    { text: 'Login fails', href: undefined },
    { text: 'Open', href: undefined },
    { text: 'sso, web', href: undefined },
  ])
  assert.equal(t.rows[1][0].href, undefined)
  assert.deepEqual(tableModel({ ...spec, items: 'missing' }, result, 'en-US').rows, [])
})

test('cards, kv and markdown views read their fields', () => {
  const cards = cardsModel({ view: 'cards', items: 'issues', title: 'key', subtitle: 'fields.status', link: 'url' }, result)
  assert.deepEqual(cards[0], {
    title: 'ENG-1', subtitle: 'Open', body: '', href: 'https://acme.atlassian.net/browse/ENG-1',
  })
  const kv = kvModel({ view: 'kv', items: 'issues.0', title: 'key', link: 'url' }, { issues: { 0: result.issues[0] } }, 'en-US')
  assert.equal(kv.title, 'ENG-1')
  assert.deepEqual(kv.rows.map((r) => r.label), ['summary', 'fields', 'labels'])
  assert.equal(kv.rows[1].text, '{"status":"Open"}')
  const picked = kvModel({ view: 'kv', fields: [{ field: 'total', title: { default: 'Total' } }] }, result, 'en-US')
  assert.deepEqual(picked.rows, [{ label: 'Total', text: '2', href: undefined }])
  assert.equal(markdownSource({ view: 'markdown', field: 'md' }, { md: '# Hi' }, 'text'), '# Hi')
  assert.equal(markdownSource({ view: 'markdown' }, {}, 'text'), 'text')
})

test('helpers', () => {
  assert.equal(valueAt({ a: { b: 1 } }, 'a.b'), 1)
  assert.equal(valueAt({ a: [1] }, 'a.0'), undefined)
  assert.equal(display(null), '')
  assert.equal(display(false), 'false')
  assert.equal(safeHref('ftp://x'), undefined)
  assert.deepEqual(toolArguments({ service: 's', tool: 't', arguments: { q: 1 } }), { q: 1 })
  assert.deepEqual(toolArguments({ q: 1 }), { q: 1 })
  assert.deepEqual(toolArguments(undefined), {})
})
