import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive } from 'vue'
import { catalogChanges, catalogRows, catalogSource, composeInput, formatTokens, hasModelOverride, patchCatalogModel, removeCatalogModelOverride, sameMembers, summarizeChanges, thinkingLevelsPatch } from './modelCatalogState'
import type { CatalogOverlay, CatalogProvider } from '@/api/system/modelCatalog'

test('form edits remove higher-precedence fields without losing unrelated overrides', () => {
  const overlay: CatalogOverlay = { providers: { openai: {
    models: [{ id: 'shared', type: 'KnowledgeQA', reasoning: true }, { id: 'shared', type: 'Embedding', dimension: 1024 }],
    model_overrides: { shared: { context_window: 123, compat: { supports_temperature: false } } },
  } } }
  const next = patchCatalogModel(overlay, 'openai', { id: 'shared', type: 'KnowledgeQA' }, { context_window: 456 })
  assert.equal(next.providers.openai.models[0].context_window, 456)
  assert.equal(next.providers.openai.models[0].reasoning, true)
  assert.equal(next.providers.openai.models[1].dimension, 1024)
  assert.deepEqual(next.providers.openai.model_overrides.shared, { compat: { supports_temperature: false } })
  assert.equal(overlay.providers.openai.model_overrides.shared.context_window, 123)
})
test('restore removes only the selected type and preserves unrelated models', () => {
  const overlay: CatalogOverlay = { providers: { openai: { models: [
    { id: 'same', type: 'KnowledgeQA', reasoning: false }, { id: 'same', type: 'Embedding', dimension: 1024 },
  ] } } }
  const next = removeCatalogModelOverride(overlay, 'openai', { id: 'same', type: 'KnowledgeQA' })
  assert.deepEqual(next.providers.openai.models, [{ id: 'same', type: 'Embedding', dimension: 1024 }])
})
test('diff identifies removed models, changed fields, and provider changes', () => {
  const before: CatalogProvider[] = [{ id: 'lab', name: 'Lab', api: 'openai-completions', model_types: ['KnowledgeQA'], models: [
    { id: 'first', context_window: 100 }, { id: 'removed' },
  ] }]
  const after = structuredClone(before)
  after[0]!.api = 'openai-responses'
  after[0]!.models = [{ id: 'first', context_window: 200 }]
  assert.deepEqual(catalogChanges(before, after).map(c => c.key), ['lab', 'lab:KnowledgeQA:first', 'lab:KnowledgeQA:removed'])
  assert.equal(catalogRows(before).length, 2)
})
test('source prefers administrator overrides, then deployment differences', () => {
  const row = { key: 'p:KnowledgeQA:m', provider: 'p', model: { id: 'm', context_window: 2 } }
  const overlay: CatalogOverlay = { providers: { p: { models: [{ id: 'M', context_window: 2 }] } } }
  assert.equal(catalogSource(overlay, row, { id: 'm', context_window: 1 }, { id: 'm', context_window: 1 }), 'console')
  assert.equal(catalogSource({ providers: {} }, row, { id: 'm', context_window: 2 }, { id: 'm', context_window: 1 }), 'deployment')
  assert.equal(catalogSource({ providers: {} }, row, { id: 'm', context_window: 2 }, { id: 'm', context_window: 2 }), 'builtin')
  assert.equal(hasModelOverride(overlay, 'p', { id: 'm', type: 'Embedding' }), true)
  assert.equal(hasModelOverride(overlay, 'p', { id: 'other' }), false)
})
test('token counts use vendor-style units', () => {
  assert.deepEqual([131072, 128000, 1048576, 1000000, 32769, undefined].map(formatTokens), ['128K', '128K', '1M', '1M', '32,769', ''])
})
test('change summaries name the model and the fields that changed', () => {
  assert.deepEqual(summarizeChanges([
    { key: 'lab', before: { api: 'a' }, after: { api: 'b' } },
    { key: 'lab:KnowledgeQA:org/x:1', before: { id: 'org/x:1', reasoning: false }, after: { id: 'org/x:1', reasoning: true } },
    { key: 'lab:Embedding:new', before: undefined, after: { id: 'new' } },
  ]), [
    { key: 'lab', provider: 'lab', target: 'lab', kind: 'provider', fields: ['api'] },
    { key: 'lab:KnowledgeQA:org/x:1', provider: 'lab', target: 'org/x:1', kind: 'updated', fields: ['reasoning'] },
    { key: 'lab:Embedding:new', provider: 'lab', target: 'new', kind: 'added', fields: [] },
  ])
})
test('edits accept the reactive overlay held by the page', () => {
  const overlay = reactive<CatalogOverlay>({ providers: { p: { models: [{ id: 'm', reasoning: true }] } } })
  assert.equal(patchCatalogModel(overlay, 'p', { id: 'm' }, { context_window: 1 }).providers.p.models[0].context_window, 1)
  assert.deepEqual(removeCatalogModelOverride(overlay, 'p', { id: 'm' }).providers.p.models, [])
})
test('input edits keep text and unknown modalities', () => {
  assert.deepEqual(composeInput(undefined, ['image']), ['text', 'image'])
  assert.deepEqual(composeInput(['text', 'image', 'pdf'], ['video']), ['text', 'pdf', 'video'])
  assert.equal(sameMembers(['a', 'b'], ['b', 'a']), true)
})
test('thinking level patches only record differences from the inherited levels', () => {
  const inherited = ['off', 'auto', 'minimal', 'low', 'medium', 'high']
  assert.equal(thinkingLevelsPatch(inherited, inherited, undefined, undefined), undefined)
  assert.deepEqual(thinkingLevelsPatch(['auto', 'low', 'medium', 'high', 'max'], inherited, undefined, { max: 'xhigh' }),
    { off: null, minimal: null, max: 'xhigh' })
  assert.deepEqual(thinkingLevelsPatch(inherited, inherited, { high: 'deep', low: null }, undefined), { high: 'deep' })
})
