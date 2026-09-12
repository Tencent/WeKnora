import assert from 'node:assert/strict'
import test from 'node:test'
import {
  evaluateToolRequirement,
  kbSatisfiesAgentRequirements,
  toolsRequireOwnedWiki,
  type ScopeCapabilities,
} from './tool-capabilities'

const scope = (ownedWiki: boolean): ScopeCapabilities => ({
  vector: false,
  keyword: false,
  wiki: true,
  ownedWiki,
  graph: false,
  faq: false,
})

test('learning tools require a Wiki knowledge base owned by the current workspace', () => {
  for (const tool of [
    'get_learning_profile',
    'recommend_learning_topics',
    'prepare_learning_quiz',
  ]) {
    assert.deepEqual(evaluateToolRequirement(tool, scope(false), true), {
      ok: false,
      missKind: 'needsOwnedWiki',
    })
    assert.deepEqual(evaluateToolRequirement(tool, scope(true), true), {
      ok: true,
      missKind: 'none',
    })
  }
})

test('single-KB filtering excludes shared Wiki knowledge bases for learning agents', () => {
  const tools = ['wiki_read_page', 'prepare_learning_quiz']
  assert.equal(toolsRequireOwnedWiki(tools), true)
  assert.equal(kbSatisfiesAgentRequirements(scope(false), 'smart-reasoning', tools), false)
  assert.equal(kbSatisfiesAgentRequirements(scope(true), 'smart-reasoning', tools), true)
  assert.equal(kbSatisfiesAgentRequirements(scope(false), 'smart-reasoning', ['wiki_read_page']), true)
})
