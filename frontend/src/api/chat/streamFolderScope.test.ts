import assert from 'node:assert/strict'
import test from 'node:test'

function installMockLocalStorage() {
  const store: Record<string, string> = {}
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (key: string) => store[key] ?? null,
      setItem: (key: string, value: string) => {
        store[key] = value
      },
      removeItem: (key: string) => {
        delete store[key]
      },
    },
    configurable: true,
  })
}

installMockLocalStorage()
const { buildStreamPostBody } = await import('./streamBody.ts')

test('knowledge chat payload includes folder_scopes paired by KB', () => {
  const body = buildStreamPostBody({
    session_id: 'session-1',
    query: 'What can the product do?',
    method: 'POST',
    url: '/api/v1/knowledge-chat',
    knowledge_base_ids: ['kb-1', 'kb-2'],
    knowledge_ids: ['file-1'],
    folder_scopes: [
      { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
    ],
    tag_ids: ['tag-1'],
    agent_enabled: false,
  })

  assert.deepEqual(body.folder_scopes, [
    { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
  ])
  assert.deepEqual(body.knowledge_base_ids, ['kb-1', 'kb-2'])
  assert.deepEqual(body.knowledge_ids, ['file-1'])
  assert.deepEqual(body.tag_ids, ['tag-1'])
  assert.equal('tenant_id' in body, false)
  assert.equal('folder_path' in body, false)
  assert.equal('folder_name' in body, false)
})

test('agent chat payload uses the same folder_scopes shape', () => {
  const body = buildStreamPostBody({
    session_id: 'session-1',
    query: 'Investigate this',
    method: 'POST',
    url: '/api/v1/agent-chat',
    knowledge_base_ids: ['kb-1', 'kb-2'],
    folder_scopes: [
      { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
      { knowledge_base_id: 'kb-2', folder_ids: ['folder-c'] },
    ],
    agent_enabled: true,
    agent_id: 'agent-1',
    mcp_service_ids: ['svc-1'],
    skill_names: ['skill-a'],
  })

  assert.equal(body.agent_enabled, true)
  assert.equal(body.agent_id, 'agent-1')
  assert.deepEqual(body.folder_scopes, [
    { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
    { knowledge_base_id: 'kb-2', folder_ids: ['folder-c'] },
  ])
  assert.equal(JSON.stringify(body.folder_scopes).includes('folder_id"'), false)
})

test('empty folder scopes are omitted so old payloads remain unchanged', () => {
  const body = buildStreamPostBody({
    session_id: 'session-1',
    query: 'Plain chat',
    method: 'POST',
    url: '/api/v1/knowledge-chat',
    folder_scopes: [],
    agent_enabled: false,
  })

  assert.equal('folder_scopes' in body, false)
})

test('embed channel still serializes without folder scopes unless provided', () => {
  const body = buildStreamPostBody({
    session_id: 'session-1',
    query: 'Embedded',
    method: 'POST',
    url: '/api/v1/embed-chat',
    embed_token: 'embed-token',
    agent_enabled: false,
  })

  assert.equal(body.channel, 'embed')
  assert.equal('folder_scopes' in body, false)
})
