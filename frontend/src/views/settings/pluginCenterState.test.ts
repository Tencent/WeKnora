import assert from 'node:assert/strict'
import test from 'node:test'

import type { TenantPlugin } from '../../api/plugin'
import { contributionSummary, filterPlugins } from './pluginCenterState'

const plugin = (id: string, name: string, contributes: TenantPlugin['manifest']['contributes'], zh?: string): TenantPlugin => ({
  enabled: true,
  manifest: {
    schemaVersion: 1, id, version: '1.0.0', publisher: { id: 'weknora' }, runtime: { type: 'builtin' },
    name: zh ? { default: name, 'zh-CN': zh } : { default: name },
    contributes,
  },
})

const feishu = plugin('weknora.feishu', 'Feishu / Lark', {
  connectors: [{ id: 'feishu', name: { default: 'Feishu' } }, { id: 'lark', name: { default: 'Lark' } }],
  imChannels: [{ id: 'feishu', name: { default: 'Feishu' } }],
}, '飞书 / Lark')
const bing = plugin('weknora.bing', 'Bing', { webSearch: [{ id: 'bing', name: { default: 'Bing' } }] })
const openai = plugin('weknora.openai', 'OpenAI', { modelVendors: [{ id: 'openai', name: { default: 'OpenAI' } }] })

test('contributionSummary counts per point in point order', () => {
  assert.deepEqual(contributionSummary(feishu.manifest), [
    { point: 'connectors', count: 2 },
    { point: 'imChannels', count: 1 },
  ])
})

test('filterPlugins matches names, IDs and contributions, and filters by point', () => {
  const all = [openai, feishu, bing]
  const names = (list: TenantPlugin[]) => list.map((p) => p.manifest.id)
  assert.deepEqual(names(filterPlugins(all, { query: '', point: '', locale: 'en-US' })),
    ['weknora.bing', 'weknora.feishu', 'weknora.openai'])
  assert.deepEqual(names(filterPlugins(all, { query: 'lark', point: '', locale: 'en-US' })), ['weknora.feishu'])
  assert.deepEqual(names(filterPlugins(all, { query: '飞书', point: '', locale: 'zh-CN' })), ['weknora.feishu'])
  assert.deepEqual(names(filterPlugins(all, { query: '', point: 'webSearch', locale: 'en-US' })), ['weknora.bing'])
  assert.deepEqual(names(filterPlugins(all, { query: 'bing', point: 'connectors', locale: 'en-US' })), [])
})
