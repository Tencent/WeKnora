import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

const componentSource = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')
const start = componentSource.indexOf('const groupedPages = computed(() => {')
const end = componentSource.indexOf('\n\n// hasContentPages is', start)
const groupedPagesSource = componentSource.slice(start, end)

function projectGroups(pagesByType: Record<string, any>, pagesByTypeStats: Record<string, number>) {
  const script = ts.transpileModule(`${groupedPagesSource}\ngroupedPages.value`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  return runInNewContext(script, {
    computed: (get: () => unknown) => ({ get value() { return get() } }),
    pagesByType: { value: pagesByType },
    stats: { value: { pages_by_type: pagesByTypeStats } },
    CONTENT_TABS: ['knowledge', 'summary'],
    KNOWLEDGE_TAB: 'knowledge',
    KNOWLEDGE_TYPES: ['entity', 'concept', 'synthesis', 'comparison'],
    getTypeLabel: (type: string) => type,
  }) as Array<{ type: string; total: number; hasMore: boolean }>
}

test('sidebar tab totals use whole-library statistics while root pagination stays directory-scoped', () => {
  const groups = projectGroups({
    knowledge: { total: 2, items: [{}], categoryPaths: [], loading: false },
    summary: { total: 1, items: [{}], categoryPaths: [], loading: false },
  }, { entity: 2, concept: 3, synthesis: 4, comparison: 6, summary: 9 })

  const knowledge = groups.find(group => group.type === 'knowledge')!
  const summary = groups.find(group => group.type === 'summary')!
  assert.equal(knowledge.total, 15)
  assert.equal(summary.total, 9)
  assert.equal(knowledge.hasMore, true)
  assert.equal(summary.hasMore, false)
})

test('zero whole-library totals do not fall back to a nonzero root-page total', () => {
  const groups = projectGroups({
    knowledge: { total: 1, items: [{}], categoryPaths: [], loading: false },
  }, { entity: 0, concept: 0, synthesis: 0, comparison: 0 })

  assert.equal(groups.length, 1)
  assert.equal(groups[0].total, 0)
})

test('empty directories remain visible without being counted as pages', () => {
  const groups = projectGroups({
    knowledge: { total: 0, items: [], categoryPaths: ['Empty folder'], loading: false },
  }, { entity: 0, concept: 0, synthesis: 0, comparison: 0 })

  assert.equal(groups.length, 1)
  assert.equal(groups[0].total, 0)
})
