import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { effectScope, nextTick, reactive, ref, watch } from 'vue'
import { selectInitialModelId } from '../utils/modelDefaults.ts'

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const selection = source.slice(source.indexOf('const initChatModelSelection ='), source.indexOf('const handleGoToConversationModels ='))
const script = ts.transpileModule(selection, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText

function fixture(t, { saved = 'retired', agent = '', shared = false, embedded = false } = {}) {
  let loaded = false
  let failLoad = false
  const availableModels = ref([])
  const selectedModelId = ref(saved)
  const agentModelId = ref(agent)
  const selectedSharedAgent = ref(shared ? {} : null)
  const scope = effectScope()
  t.after(() => scope.stop())
  const settingsStore = reactive({
    selectedAgentSourceTenantId: shared ? 'source-workspace' : '',
    get conversationModels() { return { selectedChatModelId: selectedModelId.value } },
    updateConversationModels({ selectedChatModelId }) { selectedModelId.value = selectedChatModelId },
  })
  const api = scope.run(() => runInNewContext(script + '\n({ initChatModelSelection, loadChatModels })', {
    availableModels, selectedModelId, selectedAgentId: ref('agent'), agentModelId, selectedSharedAgent,
    readLastChatModelID: () => saved, writeLastChatModelID: (value) => { saved = value },
    settingsStore, selectInitialModelId, props: { embeddedMode: embedded }, watch,
    console: { error() {} },
    chatResources: {
      isLoaded: () => loaded,
      ensureChatModels: async () => { if (failLoad) throw new Error('offline'); loaded = true },
      invalidate: () => { loaded = false },
    },
  }))
  return {
    ...api, availableModels, selectedModelId, agentModelId,
    saved: () => saved,
    failLoad: () => { failLoad = true },
  }
}

const models = ['first', 'default', 'agent'].map(id => ({
  id, type: 'KnowledgeQA', default_for: id === 'default' ? ['KnowledgeQA'] : [],
}))

test('a retired preference recovers to the agent, then follows catalog changes', async t => {
  const f = fixture(t, { agent: 'agent' })
  f.initChatModelSelection()
  f.availableModels.value = models
  await f.loadChatModels()
  assert.equal(f.selectedModelId.value, 'agent')
  assert.equal(f.saved(), '')
  f.availableModels.value = models.slice(0, 2)
  await nextTick()
  assert.equal(f.selectedModelId.value, 'default')
  f.availableModels.value = []
  await nextTick()
  assert.equal(f.selectedModelId.value, '')
})

test('a valid saved override survives local agent hydration', async t => {
  const f = fixture(t, { saved: 'first' })
  f.availableModels.value = models
  await f.loadChatModels()
  f.agentModelId.value = 'agent'
  await nextTick()
  assert.equal(f.selectedModelId.value, 'first')
  assert.equal(f.saved(), 'first')
})

test('a shared agent can retain a model absent from the local catalog', async t => {
  const f = fixture(t, { shared: true, agent: 'foreign', saved: 'foreign' })
  f.availableModels.value = models
  await f.loadChatModels()
  assert.equal(f.selectedModelId.value, 'foreign')
  assert.equal(f.saved(), 'foreign')
})

test('a failed catalog request preserves the stored preference', async t => {
  const f = fixture(t)
  f.failLoad()
  await f.loadChatModels()
  assert.equal(f.selectedModelId.value, 'retired')
  assert.equal(f.saved(), 'retired')
})

test('an embedded composer leaves the platform model and preference untouched', async t => {
  const f = fixture(t, { embedded: true, agent: 'agent' })
  f.availableModels.value = models
  await f.loadChatModels()
  await nextTick()
  assert.equal(f.selectedModelId.value, 'retired')
  assert.equal(f.saved(), 'retired')
})
