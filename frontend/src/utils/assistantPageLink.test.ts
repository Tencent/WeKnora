import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isAssistantPageLink, pageLinkDraft, pageLinkEndpoint, pageLinkLocation, pageLinkReturnPath, type AssistantPageLink,
} from './assistantPageLink'

const link = (fields: Partial<AssistantPageLink>): AssistantPageLink =>
  ({ display_type: 'open_page', page: 'knowledge_base', label: 'x', ...fields }) as AssistantPageLink

test('links use the deep links each page reads', () => {
  assert.deepEqual(pageLinkLocation(link({ knowledge_base_id: 'kb/1', knowledge_id: 'doc' })),
    { path: '/platform/knowledge-bases/kb%2F1', query: { knowledge_id: 'doc' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'wiki', knowledge_base_id: 'kb', wiki_slug: 'entity/acme' })),
    { path: '/platform/knowledge-bases/kb', query: { tab: 'wiki', slug: 'entity/acme' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'agent_editor', agent_id: 'a', section: 'tools' })),
    { path: '/platform/agents', query: { edit: 'a', section: 'tools' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'settings_models', model_id: 'm' })),
    { path: '/platform/settings', query: { section: 'models', model_id: 'm' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'mcp_services' })), { path: '/platform/toolbox/mcp', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'automations', automation_id: 'a/1' })),
    { path: '/platform/automations/a%2F1', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'automations' })), { path: '/platform/automations', query: {} })
  assert.equal(pageLinkLocation(link({ page: 'agent_editor' })), null, 'a link without its target goes nowhere')
})

test('a page the assistant opened goes back only to an in-app path', () => {
  assert.equal(pageLinkReturnPath(link({ navigated: true, return_path: '/platform/agents?x=1' })), '/platform/agents?x=1')
  assert.equal(pageLinkReturnPath(link({ return_path: '/platform/agents' })), '', 'a button has no way back')
  for (const bad of ['https://evil.example', '//evil.example', '/\\evil.example', '']) {
    assert.equal(pageLinkReturnPath(link({ navigated: true, return_path: bad })), '', bad)
  }
})

test('pages without a resource open the settings section, the tool or the list', () => {
  assert.deepEqual(pageLinkLocation(link({ page: 'settings', section: 'general' })),
    { path: '/platform/settings', query: { section: 'general' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'settings', section: 'system-global', panel: 'users' })),
    { path: '/platform/settings', query: { section: 'system-global', panel: 'users' } })
  assert.deepEqual(pageLinkLocation(link({ page: 'settings' })), { path: '/platform/settings', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'toolbox', section: 'skills' })),
    { path: '/platform/toolbox/skills', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'toolbox' })), { path: '/platform/toolbox', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'agents' })), { path: '/platform/agents', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'knowledge_bases' })), { path: '/platform/knowledge-bases', query: {} })
  assert.deepEqual(pageLinkLocation(link({ page: 'organizations' })), { path: '/platform/organizations', query: {} })
})

test('only a new model or service carries a form, and its endpoint is shown', () => {
  const model = link({ page: 'settings_models', model_draft: { type: 'chat', base_url: 'https://api.example/v1' } })
  assert.deepEqual(pageLinkDraft(model), { kind: 'model', draft: { type: 'chat', base_url: 'https://api.example/v1' } })
  assert.equal(pageLinkEndpoint(model), 'https://api.example/v1')
  assert.equal(pageLinkDraft(link({ page: 'settings_models', model_id: 'm', model_draft: { type: 'chat' } })), null)
  const mcp = link({ page: 'mcp_services', mcp_service_draft: { name: 'J', transport_type: 'sse', url: 'https://m.example' } })
  assert.equal(pageLinkDraft(mcp)?.kind, 'mcp_service')
  assert.equal(pageLinkEndpoint(mcp), 'https://m.example')
  assert.equal(pageLinkDraft(link({ knowledge_base_id: 'kb' })), null)
  assert.equal(isAssistantPageLink({ display_type: 'open_page', page: 'wiki', label: 'W' }), true)
  assert.equal(isAssistantPageLink({ display_type: 'search_results' }), false)
})
