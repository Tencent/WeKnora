import assert from 'node:assert/strict'
import { after, before, beforeEach, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createPinia } from 'pinia'
import type { SessionLastRequestStatePayload } from './settings'

let server: ViteDevServer
let useSettingsStore: typeof import('./settings').useSettingsStore
const savedStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const items = new Map<string, string>()

before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => items.get(key) ?? null,
    setItem: (key: string, value: string) => items.set(key, value),
    removeItem: (key: string) => items.delete(key),
  } })
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'offline-store-test', enforce: 'pre',
      resolveId(id) { if (id.endsWith('/utils/request')) return '\0offline-request' },
      load(id) { if (id === '\0offline-request') return 'const request = () => { throw new Error("Unexpected network request") }; export { request as get, request as post, request as put, request as del };' },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: { '@': fileURLToPath(new URL('../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  ;({ useSettingsStore } = await server.ssrLoadModule('/src/stores/settings.ts'))
})

beforeEach(() => items.clear())

after(async () => {
  await server?.close()
  if (savedStorage) Object.defineProperty(globalThis, 'localStorage', savedStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})

function selectDefaults(store: ReturnType<typeof useSettingsStore>) {
  store.settings.selectedFiles = ['default-file']
  store.settings.selectedFileKbMap = { 'default-file': 'default-kb' }
  store.settings.selectedTags = [{ id: 'default-tag', name: 'Default tag', kbId: 'default-kb' }]
  store.settings.selectedMCPServices = ['default-mcp']
  store.settings.selectedSkills = ['default-skill']
  store.selectKnowledgeBases(['default-kb'])
}

test('applying a file selection writes a deduplicated scope once and replaces only confirmed conflicting KBs', (t) => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.selectKnowledgeBases(['default-kb', 'other-kb', 'keep-whole-kb'])
  store.settings.selectedFiles.push('removed-file', 'legacy-unmapped')
  store.settings.selectedFileKbMap['removed-file'] = 'other-kb'
  const write = t.mock.method(localStorage, 'setItem')

  store.applyFileSelection([
    { id: 'default-file' },
    { id: 'new-file', kbId: 'keep-whole-kb' },
    { id: 'same-name-different-id', kbId: 'keep-whole-kb' },
    { id: 'new-file' },
    { id: 'legacy-unmapped' },
  ], ['default-kb', 'other-kb'])

  const expectedIds = ['default-file', 'new-file', 'same-name-different-id', 'legacy-unmapped']
  const expectedMap = {
    'default-file': 'default-kb',
    'new-file': 'keep-whole-kb',
    'same-name-different-id': 'keep-whole-kb',
  }
  assert.deepEqual(store.getSelectedFiles(), expectedIds)
  assert.deepEqual(store.settings.selectedFileKbMap, expectedMap)
  assert.deepEqual(store.getSelectedKnowledgeBases(), ['other-kb', 'keep-whole-kb'])
  assert.deepEqual(store.settings.selectedTags, [{ id: 'default-tag', name: 'Default tag', kbId: 'default-kb' }])
  assert.deepEqual(store.settings.selectedMCPServices, ['default-mcp'])
  assert.deepEqual(store.settings.selectedSkills, ['default-skill'])
  assert.equal(write.mock.callCount(), 1)
  assert.deepEqual(JSON.parse(items.get('WeKnora_settings')!), JSON.parse(JSON.stringify(store.settings)))
})

test('a failed bulk file selection write keeps the previous selection and whole-KB scope', (t) => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  const before = JSON.stringify(store.settings)
  const persistedBefore = items.get('WeKnora_settings')
  t.mock.method(localStorage, 'setItem', () => { throw new Error('Storage unavailable') })

  assert.throws(
    () => store.applyFileSelection([{ id: 'new-file', kbId: 'default-kb' }], ['default-kb']),
    /Storage unavailable/,
  )
  assert.equal(JSON.stringify(store.settings), before)
  assert.equal(items.get('WeKnora_settings'), persistedBefore)
})

test('history restores file ownership from selected file mentions and keeps the legacy mapping fallback', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  const persistedBefore = items.get('WeKnora_settings')

  store.hydrateSessionInputState({
    knowledge_ids: ['default-file', 'shared-file', 'unmapped'],
    mentioned_items: [
      { id: 'shared-file', type: 'file', kb_id: 'shared-kb' },
      { id: 'unselected', type: 'file', kb_id: 'unselected-kb' },
      { id: 'unmapped', type: 'tag', kb_id: 'tag-kb' },
    ],
  })

  assert.deepEqual(store.getSelectedFiles(), ['default-file', 'shared-file', 'unmapped'])
  assert.deepEqual(store.settings.selectedFileKbMap, { 'default-file': 'default-kb', 'shared-file': 'shared-kb' })
  assert.deepEqual(store.getSelectedKnowledgeBases(), [])
  assert.equal(items.get('WeKnora_settings'), persistedBefore)
})

test('history restores the session agent and source tenant without changing browser defaults', () => {
  const store = useSettingsStore(createPinia())
  store.selectAgent('default-shared', '12')
  const storedDefaults = items.get('WeKnora_settings')

  store.hydrateSessionInputState({ agent_id: 'session-shared', agent_source_tenant_id: '42' })

  assert.equal(store.selectedAgentId, 'session-shared')
  assert.equal(store.selectedAgentSourceTenantId, '42')
  assert.equal(items.get('WeKnora_settings'), storedDefaults)
  store.restoreDefaultsIfSnapshotted()
  assert.equal(store.selectedAgentId, 'default-shared')
  assert.equal(store.selectedAgentSourceTenantId, '12')
})

test('history without an Agent ID restores the local mode instead of the shared browser default', () => {
  for (const enabled of [false, true]) {
    const store = useSettingsStore(createPinia())
    store.selectAgent('default-shared', '12')
    store.hydrateSessionInputState({ agent_enabled: enabled, knowledge_ids: ['file'] })
    assert.equal(store.selectedAgentId, enabled ? 'builtin-smart-reasoning' : 'builtin-quick-answer')
    assert.equal(store.selectedAgentSourceTenantId, null)
    assert.deepEqual(store.settings.selectedFiles, ['file'])
    store.restoreDefaultsIfSnapshotted()
    assert.equal(store.selectedAgentSourceTenantId, '12')
  }
})

test('verified legacy share updates only the matching hydrated session agent', () => {
  const store = useSettingsStore(createPinia())
  store.selectAgent('default-shared', '12')
  store.hydrateSessionInputState({ agent_id: 'legacy-shared', knowledge_ids: ['file'] })
  store.resolveLegacySessionAgentSource('other-agent', '77')
  assert.equal(store.selectedAgentSourceTenantId, null)
  store.resolveLegacySessionAgentSource('legacy-shared', '42')
  assert.equal(store.selectedAgentSourceTenantId, '42')
  assert.deepEqual(store.settings.selectedFiles, ['file'])
  store.restoreDefaultsIfSnapshotted()
  assert.equal(store.selectedAgentSourceTenantId, '12')
})

test('legacy history without a source tenant does not reuse the default shared tenant', () => {
  const store = useSettingsStore(createPinia())
  store.selectAgent('default-shared', '12')

  store.hydrateSessionInputState({ agent_id: 'local-session-agent' })

  assert.equal(store.selectedAgentId, 'local-session-agent')
  assert.equal(store.selectedAgentSourceTenantId, null)
})

test('a new chat clears prior KB and file mentions while keeping unrelated composer settings', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.settings.selectedAgentId = 'agent-1'
  store.settings.selectedMCPServices = ['mcp-1']

  store.clearChatReferences()

  assert.deepEqual(store.getSelectedKnowledgeBases(), [])
  assert.deepEqual(store.getSelectedFiles(), [])
  assert.deepEqual(store.settings.selectedFileKbMap, {})
  assert.deepEqual(store.settings.selectedTags, [])
  assert.equal(store.settings.selectedAgentId, 'agent-1')
  assert.deepEqual(store.settings.selectedMCPServices, ['mcp-1'])
  assert.deepEqual(JSON.parse(items.get('WeKnora_settings')!).selectedKnowledgeBases, [])
  assert.deepEqual(JSON.parse(items.get('WeKnora_settings')!).selectedFiles, [])
})

test('search-started chat replaces old references and survives session default restoration', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.snapshotAsDefaultsIfNeeded()

  store.prepareNewChatReferences(['new-kb'], ['new-file'])
  store.restoreDefaultsIfSnapshotted()
  const references = store.consumeNewChatReferences()
  assert.deepEqual(references, { kbIds: ['new-kb'], fileIds: ['new-file'] })
  store.setNewChatReferences(references!.kbIds, references!.fileIds)

  assert.deepEqual(store.getSelectedKnowledgeBases(), ['new-kb'])
  assert.deepEqual(store.getSelectedFiles(), ['new-file'])
  assert.deepEqual(store.settings.selectedFileKbMap, {})
  assert.equal(store.consumeNewChatReferences(), null)
})

const emptySelections: SessionLastRequestStatePayload = {
  knowledge_base_ids: [], knowledge_ids: [], tag_ids: [], mcp_service_ids: [], skill_names: [],
}

for (const state of [{ agent_enabled: true, knowledge_base_ids: [] }, emptySelections]) {
  test(`session hydration clears stale file, tag, and tool selections when saved lists are ${'knowledge_ids' in state ? 'empty' : 'omitted'}`, () => {
    const store = useSettingsStore(createPinia())
    selectDefaults(store)
    const storedDefaults = items.get('WeKnora_settings')

    store.hydrateSessionInputState(state)

    assert.deepEqual(store.getSelectedKnowledgeBases(), [])
    assert.deepEqual(store.getSelectedFiles(), [])
    assert.deepEqual(store.settings.selectedFileKbMap, {})
    assert.deepEqual(store.settings.selectedTags, [])
    assert.deepEqual(store.settings.selectedMCPServices, [])
    assert.deepEqual(store.settings.selectedSkills, [])
    assert.deepEqual(store.getSuggestedQuestionsParams(), {
      knowledge_base_ids: undefined, knowledge_ids: undefined, tag_scopes: undefined, limit: undefined,
    })
    assert.equal(items.get('WeKnora_settings'), storedDefaults, 'hydration must not overwrite browser defaults')
    store.restoreDefaultsIfSnapshotted()
    assert.deepEqual(JSON.parse(JSON.stringify(store.settings)), JSON.parse(storedDefaults!))
  })
}

test('switching back to a session without mentions clears file and tool selections from browser defaults', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.hydrateSessionInputState({ knowledge_base_ids: [] })

  // The chat route restores defaults before loading every other session.
  store.restoreDefaultsIfSnapshotted()
  store.hydrateSessionInputState({
    knowledge_ids: ['other-session-file'], mcp_service_ids: ['other-session-mcp'], skill_names: ['other-session-skill'],
  })
  assert.deepEqual(store.getSelectedFiles(), ['other-session-file'])
  assert.deepEqual(store.settings.selectedMCPServices, ['other-session-mcp'])
  assert.deepEqual(store.settings.selectedSkills, ['other-session-skill'])
  store.restoreDefaultsIfSnapshotted()
  store.hydrateSessionInputState({ knowledge_base_ids: [] })

  assert.deepEqual(store.getSelectedFiles(), [])
  assert.deepEqual(store.settings.selectedTags, [])
  assert.deepEqual(store.settings.selectedMCPServices, [])
  assert.deepEqual(store.settings.selectedSkills, [])
  store.restoreDefaultsIfSnapshotted()
  assert.deepEqual(store.getSelectedFiles(), ['default-file'])
  assert.deepEqual(store.settings.selectedTags, [{ id: 'default-tag', name: 'Default tag', kbId: 'default-kb' }])
  assert.deepEqual(store.settings.selectedMCPServices, ['default-mcp'])
  assert.deepEqual(store.settings.selectedSkills, ['default-skill'])
})

test('session hydration copies explicit selections and retains only selected files in the KB map', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  const state = {
    knowledge_base_ids: ['session-kb'], knowledge_ids: ['session-file', 'default-file'],
    mcp_service_ids: ['session-mcp'], skill_names: ['session-skill'],
  }
  store.settings.selectedFileKbMap['unselected-file'] = 'another-kb'

  store.hydrateSessionInputState(state)
  assert.deepEqual(store.getSelectedKnowledgeBases(), ['session-kb'])
  assert.deepEqual(store.getSelectedFiles(), ['session-file', 'default-file'])
  assert.deepEqual(store.settings.selectedFileKbMap, { 'default-file': 'default-kb' })
  assert.deepEqual(store.settings.selectedMCPServices, ['session-mcp'])
  assert.deepEqual(store.settings.selectedSkills, ['session-skill'])
  store.settings.selectedKnowledgeBases.push('another-kb')
  store.settings.selectedFiles.push('another-file')
  store.settings.selectedMCPServices.push('another-mcp')
  store.settings.selectedSkills.push('another-skill')

  assert.deepEqual(state.knowledge_base_ids, ['session-kb'])
  assert.deepEqual(state.knowledge_ids, ['session-file', 'default-file'])
  assert.deepEqual(state.mcp_service_ids, ['session-mcp'])
  assert.deepEqual(state.skill_names, ['session-skill'])
  store.restoreDefaultsIfSnapshotted()
  assert.deepEqual(store.getSelectedKnowledgeBases(), ['default-kb'])
})

test('session hydration restores tags and tool selections from mentions when ID lists are omitted', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.hydrateSessionInputState({
    knowledge_base_ids: ['session-kb'],
    mentioned_items: [
      { id: 'tag', type: 'tag', name: 'Saved tag', kb_id: 'session-kb', kb_name: 'Saved KB' },
      { id: 'mcp', type: 'mcp' },
      { id: 'skill-id', type: 'skill', skill_name: 'saved-skill' },
    ],
  })

  assert.deepEqual(store.settings.selectedTags, [{ id: 'tag', name: 'Saved tag', kbId: 'session-kb', kbName: 'Saved KB' }])
  assert.deepEqual(store.settings.selectedMCPServices, ['mcp'])
  assert.deepEqual(store.settings.selectedSkills, ['saved-skill'])
})

test('explicit empty ID lists override stale tag and tool mentions', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  store.hydrateSessionInputState({
    ...emptySelections,
    mentioned_items: [
      { id: 'tag', type: 'tag', kb_id: 'old-kb' },
      { id: 'mcp', type: 'mcp' },
      { id: 'skill', type: 'skill' },
    ],
  })

  assert.deepEqual(store.settings.selectedTags, [])
  assert.deepEqual(store.settings.selectedMCPServices, [])
  assert.deepEqual(store.settings.selectedSkills, [])
})

test('explicit tag IDs filter mentions and preserve the single-KB fallback for missing metadata', () => {
  const store = useSettingsStore(createPinia())
  store.hydrateSessionInputState({
    knowledge_base_ids: ['session-kb'], tag_ids: ['selected-tag', 'orphan-tag'],
    mentioned_items: [
      { id: 'selected-tag', type: 'tag', name: 'Saved tag', kb_id: 'session-kb' },
      { id: 'unselected-tag', type: 'tag', kb_id: 'other-kb' },
    ],
  })

  assert.deepEqual(store.settings.selectedTags, [
    { id: 'selected-tag', name: 'Saved tag', kbId: 'session-kb', kbName: undefined },
    { id: 'orphan-tag', name: 'orphan-tag', kbId: 'session-kb', kbName: undefined },
  ])
})

test('missing session state and first-send hydration preserve the current draft selections', () => {
  const store = useSettingsStore(createPinia())
  selectDefaults(store)
  const draft = JSON.stringify(store.settings)

  store.hydrateSessionInputState(null)
  store.hydrateSessionInputState(undefined)
  store.hydrateSessionInputState({ agent_enabled: true }, true)

  assert.equal(JSON.stringify(store.settings), draft)
  assert.equal(store._defaultsSnapshot, null)
})
