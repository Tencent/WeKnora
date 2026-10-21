import assert from 'node:assert/strict'
import test from 'node:test'

import type { AgentToolCatalog } from '../api/agent'
import {
  allowedToolsToSave,
  buildToolOptions,
  effectiveToolSelection,
  resolveLoadedAllowedTools,
  storedExtraTools,
} from './agent-tool-list.ts'
import type { ScopeCapabilities } from './tool-capabilities.ts'

// Shaped like GET /api/v1/agents/tools, trimmed to the cases under test.
const catalog: AgentToolCatalog = {
  tools: [
    { name: 'thinking', label: '思考', description: 'd', group: 'base', requirement: {} },
    { name: 'search_conversations', label: '回顾历史对话', description: 'd', group: 'base', requirement: {} },
    { name: 'search_knowledge', label: '检索知识库', description: 'd', group: 'rag', requirement: { any_of: ['vector', 'keyword'] } },
    { name: 'read_document', label: '阅读文档', description: 'd', group: 'rag', requirement: { any_of: ['vector', 'keyword', 'wiki'] } },
    { name: 'wiki_delete_page', label: '删除Wiki', description: 'd', group: 'wiki_edit', danger: true, requirement: { all_of: ['wiki'] } },
  ],
  default_tools: ['search_knowledge', 'read_document', 'search_conversations'],
  switch_managed_tools: ['web_search', 'search_memory'],
}

const noKb: ScopeCapabilities = { vector: false, keyword: false, wiki: false, graph: false, faq: false }
const ragKb: ScopeCapabilities = { ...noKb, vector: true, keyword: true }
const noText = () => undefined

test('every catalog tool becomes a checkbox, including ones the editor has no translation for', () => {
  const options = buildToolOptions(catalog, [], ragKb, true, (name) =>
    name === 'thinking' ? { label: 'Thinking', description: 'Reflect' } : undefined,
  )
  assert.deepEqual(options.map((o) => o.value), catalog.tools.map((t) => t.name))
  const thinking = options.find((o) => o.value === 'thinking')!
  assert.equal(thinking.label, 'Thinking')
  const conversations = options.find((o) => o.value === 'search_conversations')!
  assert.equal(conversations.label, '回顾历史对话')
  assert.equal(options.find((o) => o.value === 'wiki_delete_page')!.danger, true)
})

test('a missing capability blocks ticking a tool but never unticking it', () => {
  const unticked = buildToolOptions(catalog, [], noKb, false, noText).find((o) => o.value === 'search_knowledge')!
  assert.equal(unticked.unmet, true)
  assert.equal(unticked.missKind, 'needsKb')
  assert.equal(unticked.disabled, true)

  // The knowledge-base-less agent that stores search_knowledge must be able to untick it.
  const ticked = buildToolOptions(catalog, ['search_knowledge'], noKb, false, noText).find((o) => o.value === 'search_knowledge')!
  assert.equal(ticked.unmet, true)
  assert.equal(ticked.disabled, false)
})

test('stored names the catalog does not offer stay visible and removable', () => {
  const stored = ['search_knowledge', 'web_search', 'none', 'search_memory', 'none']
  const extras = storedExtraTools(catalog, stored, stored)
  assert.deepEqual(extras, [
    { value: 'web_search', kind: 'switch_managed', disabled: false },
    { value: 'none', kind: 'unrecognized', disabled: false },
    { value: 'search_memory', kind: 'switch_managed', disabled: false },
  ])

  // Unticking keeps the entry on screen, but it cannot be ticked again.
  const afterUntick = storedExtraTools(catalog, stored, ['search_knowledge'])
  assert.deepEqual(afterUntick.map((e) => [e.value, e.disabled]), [
    ['web_search', true], ['none', true], ['search_memory', true],
  ])
})

test('without a catalog nothing is classified, so stored values are left alone', () => {
  assert.deepEqual(buildToolOptions(null, ['none'], ragKb, true, noText), [])
  assert.deepEqual(storedExtraTools(null, ['none'], ['none']), [])
})

test('an unset list shows the default tools the runtime uses', () => {
  const withKb = effectiveToolSelection(catalog, null, ragKb, true)
  assert.equal(withKb.usesDefaults, true)
  assert.deepEqual(withKb.names, ['search_conversations', 'search_knowledge', 'read_document'])
  assert.deepEqual(withKb.inactive, [])

  // Without a knowledge base the runtime drops the retrieval defaults, so an
  // agent that never set a list ends up with search_conversations alone.
  const withoutKb = effectiveToolSelection(catalog, undefined, noKb, false)
  assert.deepEqual(withoutKb.names, ['search_conversations'])
  assert.deepEqual(withoutKb.inactive, [])
})

test('a non-empty list registers the ticked tools the scope can serve and ignores the rest', () => {
  const selection = effectiveToolSelection(catalog, ['search_knowledge', 'thinking', 'web_search', 'none'], noKb, false)
  assert.equal(selection.usesDefaults, false)
  assert.deepEqual(selection.names, ['thinking'])
  assert.deepEqual(selection.inactive, ['search_knowledge'])
})

test('an empty list registers no built-in tool', () => {
  const selection = effectiveToolSelection(catalog, [], ragKb, true)
  assert.equal(selection.usesDefaults, false)
  assert.deepEqual(selection.names, [])
})

test('a list holding only names the catalog does not offer registers no default tool', () => {
  const selection = effectiveToolSelection(catalog, ['none', 'web_search'], ragKb, true)
  assert.equal(selection.usesDefaults, false)
  assert.deepEqual(selection.names, [])
  assert.deepEqual(selection.inactive, [])
})

test('an unset list loads as the ticked default tools of a smart-reasoning agent', () => {
  assert.deepEqual(resolveLoadedAllowedTools(null, 'smart-reasoning', catalog),
    { tools: catalog.default_tools, keepUnset: false })
  assert.deepEqual(resolveLoadedAllowedTools(undefined, 'smart-reasoning', catalog),
    { tools: catalog.default_tools, keepUnset: false })
})

test('an unset list stays unset without a catalog or outside smart-reasoning', () => {
  for (const [mode, cat] of [['smart-reasoning', null], ['quick-answer', catalog], [undefined, catalog]] as const) {
    const loaded = resolveLoadedAllowedTools(null, mode, cat)
    assert.deepEqual(loaded, { tools: [], keepUnset: true })
    assert.equal(allowedToolsToSave(loaded.tools, loaded.keepUnset), null, 'an untouched unset list saves as null')
    assert.deepEqual(allowedToolsToSave(['thinking'], loaded.keepUnset), ['thinking'], 'a ticked tool is saved as a list')
  }
})

test('a stored list, empty or not, loads and saves as it is', () => {
  assert.deepEqual(resolveLoadedAllowedTools([], 'smart-reasoning', catalog), { tools: [], keepUnset: false })
  assert.deepEqual(allowedToolsToSave([], false), [])
  assert.deepEqual(resolveLoadedAllowedTools(['knowledge_search'], 'smart-reasoning', catalog),
    { tools: ['knowledge_search'], keepUnset: false })
})
