import assert from 'node:assert/strict'
import test from 'node:test'
import {
  createQueryGeneration,
  formatKnowledgeFolderRouteQuery,
  parseKnowledgeFolderRouteQuery,
  shouldCommitQueryResult,
  stableFiltersSignature,
} from './queryContext.ts'

test('parses folder_id and q from URL query without exposing root sentinel', () => {
  assert.deepEqual(parseKnowledgeFolderRouteQuery({}), { folderId: '', searchTerm: '' })
  assert.deepEqual(parseKnowledgeFolderRouteQuery({ folder_id: 'f1', q: 'roadmap' }), {
    folderId: 'f1',
    searchTerm: 'roadmap',
  })
})

test('trims whitespace and coerces non-string values to empty state', () => {
  assert.deepEqual(parseKnowledgeFolderRouteQuery({ folder_id: '  f2  ', q: '\tquery ' }), {
    folderId: 'f2',
    searchTerm: 'query',
  })
  assert.deepEqual(parseKnowledgeFolderRouteQuery({ folder_id: 42, q: null, extra: true }), {
    folderId: '',
    searchTerm: '',
  })
})

test('never surfaces the __root__ sentinel from URL into UI state', () => {
  assert.deepEqual(parseKnowledgeFolderRouteQuery({ folder_id: '__root__' }), {
    folderId: '',
    searchTerm: '',
  })
})

test('formats root by omitting folder_id and keeps q as search state', () => {
  assert.deepEqual(formatKnowledgeFolderRouteQuery('', ''), {})
  assert.deepEqual(formatKnowledgeFolderRouteQuery('f1', 'roadmap'), {
    folder_id: 'f1',
    q: 'roadmap',
  })
  assert.deepEqual(formatKnowledgeFolderRouteQuery('', 'roadmap'), { q: 'roadmap' })
})

test('format trims and omits empty fragments so root stays implicit', () => {
  assert.deepEqual(formatKnowledgeFolderRouteQuery('   ', '   '), {})
  assert.deepEqual(formatKnowledgeFolderRouteQuery(' f2 ', ' '), { folder_id: 'f2' })
})

test('round-trips parse and format without introducing the root sentinel', () => {
  const formatted = formatKnowledgeFolderRouteQuery('f3', 'alpha')
  const parsed = parseKnowledgeFolderRouteQuery(formatted)
  assert.deepEqual(parsed, { folderId: 'f3', searchTerm: 'alpha' })
  assert.deepEqual(formatKnowledgeFolderRouteQuery(parsed.folderId, parsed.searchTerm), formatted)
})

test('stableFiltersSignature is order-independent and stable for equal content', () => {
  const a = stableFiltersSignature({ tag: 't1', type: 'pdf', page: 1 })
  const b = stableFiltersSignature({ type: 'pdf', tag: 't1', page: 1 })
  assert.equal(a, b)
  assert.notEqual(a, stableFiltersSignature({ tag: 't1', type: 'pdf', page: 2 }))
})

test('stableFiltersSignature normalizes nested object key order but preserves array order', () => {
  // Object key order is normalized, so these are equal.
  const a = stableFiltersSignature({ nested: { b: 2, a: 1 }, tags: ['t1', 't2'] })
  const b = stableFiltersSignature({ nested: { a: 1, b: 2 }, tags: ['t1', 't2'] })
  assert.equal(a, b)
  // Array order is content, so a reordered array is a different signature.
  assert.notEqual(a, stableFiltersSignature({ nested: { a: 1, b: 2 }, tags: ['t2', 't1'] }))
})

test('generation starts empty and accepts only the current query context results', () => {
  const generation = createQueryGeneration()
  assert.equal(generation.current(), null)
  const first = generation.next({ kbId: 'kb', folderId: '', searchTerm: '', filtersSignature: 'a' })
  const second = generation.next({ kbId: 'kb', folderId: 'f1', searchTerm: '', filtersSignature: 'a' })
  assert.equal(first.generation, 1)
  assert.equal(second.generation, 2)
  assert.deepEqual(generation.current(), second)
  assert.equal(shouldCommitQueryResult(generation.current(), first), false)
  assert.equal(shouldCommitQueryResult(generation.current(), second), true)
})

test('shouldCommitQueryResult rejects stale candidates from a different generation', () => {
  const primary = createQueryGeneration()
  const stale = primary.next({ kbId: 'kb', folderId: '', searchTerm: '', filtersSignature: 'a' })
  primary.next({ kbId: 'kb', folderId: 'f1', searchTerm: '', filtersSignature: 'a' })
  const other = createQueryGeneration()
  const otherCtx = other.next({ kbId: 'kb', folderId: '', searchTerm: '', filtersSignature: 'a' })
  assert.equal(shouldCommitQueryResult(primary.current(), stale), false)
  assert.equal(shouldCommitQueryResult(primary.current(), otherCtx), false)
})

test('shouldCommitQueryResult rejects when no current context exists', () => {
  const generation = createQueryGeneration()
  assert.equal(generation.current(), null)
  const other = createQueryGeneration()
  const candidate = other.next({ kbId: 'kb', folderId: '', searchTerm: '', filtersSignature: 'a' })
  assert.equal(shouldCommitQueryResult(generation.current(), candidate), false)
})
