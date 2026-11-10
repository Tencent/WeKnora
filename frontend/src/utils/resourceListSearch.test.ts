import assert from 'node:assert/strict'
import test from 'node:test'
import { matchesResourceQuery } from './resourceListSearch'

test('search accepts empty queries and resources without descriptions', () => {
  assert.equal(matchesResourceQuery({ name: '知识库' }, '  '), true)
  assert.equal(matchesResourceQuery({ name: '知识库' }, '知识'), true)
  assert.equal(matchesResourceQuery(undefined, '知识'), false)
})

test('search matches all terms across the visible name and description', () => {
  const resource = { name: 'Wiki 数据分析', description: 'Sales REPORT' }
  assert.equal(matchesResourceQuery(resource, '  wiki   report '), true)
  assert.equal(matchesResourceQuery(resource, '数据 分析'), true)
  assert.equal(matchesResourceQuery(resource, 'wiki missing'), false)
})

test('filtering preserves references and ordering used by pins and row actions', () => {
  const rows = [{ name: 'Pinned FAQ' }, { name: 'Wiki' }, { name: 'Other FAQ' }]
  const result = rows.filter(row => matchesResourceQuery(row, 'faq'))
  assert.deepEqual(result, [rows[0], rows[2]])
  assert.equal(result[0], rows[0])
  assert.equal(result[1], rows[2])
})
