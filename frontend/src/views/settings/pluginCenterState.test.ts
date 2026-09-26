import assert from 'node:assert/strict'
import test from 'node:test'

import type { TenantPlugin } from '../../api/plugin'
import { contributionSummary, filterPlugins, hasTenantConfig, pluginSkillChoices } from './pluginCenterState'

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

test('hasTenantConfig needs a tenant schema with fields', () => {
  const m = feishu.manifest
  assert.equal(hasTenantConfig(m), false)
  assert.equal(hasTenantConfig({ ...m, config: { tenantSchema: { type: 'object', properties: {} } } }), false)
  assert.equal(hasTenantConfig({
    ...m, config: { tenantSchema: { type: 'object', properties: { api_key: { type: 'string' } } } },
  }), true)
})

test('pluginSkillChoices lists skills of enabled plugins with their install source', () => {
  const kit = plugin('acme.kit', 'ACME Kit', {
    skills: [
      { id: 'triage', name: { default: 'Triage', 'zh-CN': '分诊' }, description: { default: 'Sort issues' } },
      { id: 'audit', name: { default: 'Audit' } },
    ],
  })
  const off = { ...plugin('acme.off', 'Off', { skills: [{ id: 'x', name: { default: 'X' } }] }), enabled: false }
  assert.deepEqual(pluginSkillChoices([kit, off, bing], 'en-US').map((c) => [c.source, c.name]), [
    ['plugin:acme.kit/audit', 'Audit'],
    ['plugin:acme.kit/triage', 'Triage'],
  ])
  const zh = pluginSkillChoices([kit], 'zh-CN').find((c) => c.source === 'plugin:acme.kit/triage')
  assert.equal(zh?.name, '分诊')
  assert.equal(zh?.description, 'Sort issues')
})

test('canConfigure covers webhooks, and webhookUrl prefers the server URL', async () => {
  const { canConfigure, hasWebhooks, webhookUrl } = await import('./pluginCenterState')
  const bare = plugin('acme.bare', 'Bare', {}).manifest
  assert.equal(canConfigure(bare), false)
  const hooks = plugin('acme.hooks', 'Hooks', { webhooks: [{ id: 'inbox', name: { default: 'Inbox' } }] }).manifest
  assert.equal(hasWebhooks(hooks), true)
  assert.equal(canConfigure(hooks), true)
  assert.equal(webhookUrl({ path: '/api/v1/plugin-callbacks/a/b/c' }, 'https://x.example'), 'https://x.example/api/v1/plugin-callbacks/a/b/c')
  assert.equal(webhookUrl({ path: '/p', url: 'https://public.example/p' }, 'https://x.example'), 'https://public.example/p')
})
