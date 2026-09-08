import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

test('overview displays current knowledge count instead of cross-video relation count', () => {
  const relatedKnowledge = readFileSync(new URL('./RelatedKnowledge.vue', import.meta.url), 'utf8')
  const overviewCard = readFileSync(new URL('./RelationOverviewCard.vue', import.meta.url), 'utf8')

  assert.match(relatedKnowledge, /<RelationOverviewCard[^>]*:knowledge-count="anchors\.length"/)
  assert.match(overviewCard, /knowledgeCount/)
  assert.doesNotMatch(overviewCard, /<strong>\{\{\s*overview\.relation_count\s*\}\}<\/strong>\s*<span>个关联知识<\/span>/)
})
