import assert from 'node:assert/strict'
import test from 'node:test'
import { readDocumentReferences } from './readDocumentReferences'
import { buildReferenceList, resolveReferenceSource } from './referenceSources'
import type { KnowledgeChunksListData } from '../types/tool-results'

const legacy: KnowledgeChunksListData = {
  display_type: 'knowledge_chunks_list',
  knowledge_id: 'doc-1',
  knowledge_title: 'Proposal.pptx',
  document: { knowledge_id: 'doc-1', file_name: 'Proposal.pptx' },
  fetched_chunks: 14,
  total_chunks: 14,
}
const summary = 'Listed 14/14 chunks from Proposal.pptx (content omitted from history)'
const citation = { chunkId: 'cited-chunk', documentTitle: 'Proposal.pptx', knowledgeBaseId: 'kb-1', anchorText: '一期功能' }

test('legacy compacted read_document resolves the actual citation without a saved KB id', () => {
  const refs = readDocumentReferences(legacy, summary, 'Read document')
  const source = resolveReferenceSource(refs, citation)!
  assert.equal(source.chunkId, 'cited-chunk')
  assert.equal(source.knowledgeId, 'doc-1')
  assert.equal(source.knowledgeBaseId, 'kb-1')
  assert.equal(source.fileName, 'Proposal.pptx')
  assert.equal(source.anchorText, '一期功能')
  assert.equal(source.content, undefined)
})

test('a legacy document card never turns a document ID or summary into a passage', () => {
  const [item] = buildReferenceList(readDocumentReferences(legacy, summary, 'Read document'))
  assert.equal(item!.knowledgeId, 'doc-1')
  assert.equal(item!.chunkId, undefined)
  assert.deepEqual(item!.chunkIds, [])
  assert.equal(item!.content, undefined)
})

test('compact live and restored results retain every chunk identity and a positioned source', () => {
  const refs = readDocumentReferences({ ...legacy, knowledge_base_id: 'kb-1',
    chunk_ids: ['first-chunk', 'cited-chunk'], source_chunk_id: 'cited-chunk',
  }, '', 'Read document')
  const [item] = buildReferenceList(refs)
  assert.equal(item!.chunkId, 'first-chunk')
  assert.equal(item!.sourceChunkId, 'cited-chunk')
  assert.deepEqual(item!.chunkIds, ['first-chunk', 'cited-chunk'])
  assert.equal(resolveReferenceSource(refs, citation)!.chunkId, 'cited-chunk')
})

test('full tool results preserve chunk identities even without body text', () => {
  const refs = readDocumentReferences({ ...legacy, knowledge_base_id: 'kb-1', chunks: [
    { chunk_id: 'first-chunk', content: 'First passage' },
    { chunk_id: 'cited-chunk' },
  ] }, '', 'Read document')
  assert.deepEqual(buildReferenceList(refs)[0]!.chunkIds, ['first-chunk', 'cited-chunk'])
  assert.equal(resolveReferenceSource(refs, citation)!.chunkId, 'cited-chunk')
})

test('legacy title recovery rejects conflicting KBs and ambiguous documents', () => {
  const refs = readDocumentReferences({ ...legacy, knowledge_base_id: 'other-kb' }, summary, '')
  assert.equal(resolveReferenceSource(refs, citation), null)
  const unknown = readDocumentReferences(legacy, summary, '')
  assert.equal(resolveReferenceSource([...unknown, { ...unknown[0], id: 'doc-2', knowledge_id: 'doc-2' }], citation), null)
  // A known match must not silently win over an unknown, same-titled document.
  assert.equal(resolveReferenceSource([...unknown, { ...unknown[0], id: 'doc-2', knowledge_id: 'doc-2', knowledge_base_id: 'kb-1' }], citation), null)
})

test('FAQ references retain their chunk identity and do not open an original file', () => {
  const refs = readDocumentReferences({ ...legacy, faq_id: 'faq-1', faq_question: 'Question' }, summary, '')
  assert.equal(buildReferenceList(refs)[0]!.chunkId, 'faq-1')
  assert.equal(resolveReferenceSource(refs, { chunkId: 'faq-1' }), null)
})
