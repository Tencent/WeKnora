import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveCitationChunkId, preprocessCitationTags } from './citationMarkdown.ts'

const first = '00000001-0000-4000-8000-000000000001'
const second = '00000002-0000-4000-8000-000000000002'
const refs = [
  { id: first, knowledge_title: 'Handbook', chunk_type: 'text', knowledge_base_id: 'kb' },
  { id: second, knowledge_title: 'Handbook', chunk_type: 'text', knowledge_base_id: 'kb' },
]

for (const id of ['2', 'DOC-2', 'doc-2']) {
  test(`explicit ${id} selects the second passage of the same document`, () => {
    assert.equal(resolveCitationChunkId(id, { doc: 'Handbook', kbId: 'kb' }, refs), second)
  })
}

test('FAQ indices refer to FAQ order, not the first matching document title', () => {
  const mixed = [refs[0], ...refs.map(ref => ({ ...ref, chunk_type: 'faq' }))]
  assert.equal(resolveCitationChunkId('FAQ-2', { doc: 'Handbook' }, mixed), second)
})

test('DOC indices exclude FAQ and web search references', () => {
  const mixed = [{ ...refs[0], chunk_type: 'faq' }, { ...refs[0], chunk_type: 'web_search' }, ...refs]
  assert.equal(resolveCitationChunkId('DOC-2', { doc: 'Handbook' }, mixed), second)
})

test('numeric context indices take precedence even for numeric document titles', () => {
  const numbered = refs.map(ref => ({ ...ref, knowledge_title: '2' }))
  assert.equal(resolveCitationChunkId('2', { doc: '2' }, numbered), second)
})

test('unknown or out-of-range indices retain title recovery', () => {
  for (const id of ['missing', '99', 'FAQ-99', 'DOC-99', '0']) {
    assert.equal(resolveCitationChunkId(id, { doc: 'Handbook' }, refs), first)
  }
})

test('UUID and unavailable-reference behavior is unchanged', () => {
  assert.equal(resolveCitationChunkId(second, { doc: 'Handbook' }, refs), second)
  assert.equal(resolveCitationChunkId('DOC-2', { doc: 'Handbook' }, []), 'DOC-2')
  assert.equal(resolveCitationChunkId('', { doc: 'Handbook' }, refs), '')
  assert.equal(resolveCitationChunkId('invalid', {}, refs), 'invalid')
})

test('rendered citation targets the indexed passage, not the first title match', () => {
  const html = preprocessCitationTags('<kb doc="Handbook" chunk_id="DOC-2" kb_id="kb"/>', refs)
  assert.ok(html.includes(`data-chunk-id="${second}"`))
  assert.ok(!html.includes(`data-chunk-id="${first}"`))
})

test('a conflicting index still recovers the named document', () => {
  const distinct = [refs[0], { ...refs[1], knowledge_title: 'Other' }]
  assert.equal(resolveCitationChunkId('2', { doc: 'Handbook' }, distinct), first)
})

test('an index remains usable when document metadata is unavailable', () => {
  assert.equal(resolveCitationChunkId('2', { doc: 'Handbook' }, refs.map(({ id }) => ({ id }))), second)
})
