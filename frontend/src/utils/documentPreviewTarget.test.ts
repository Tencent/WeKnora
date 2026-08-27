import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildKnowledgeDocumentPreviewTarget,
  inferDocumentFileType,
} from './documentPreviewTarget.ts'

test('prefers server file type and falls back to the title extension', () => {
  assert.equal(inferDocumentFileType('paper.pdf', 'docx'), 'docx')
  assert.equal(inferDocumentFileType('paper.XLSX'), 'xlsx')
})

test('does not guess PDF when a historical document has no type or extension', () => {
  assert.equal(inferDocumentFileType('untitled document'), '')
  assert.deepEqual(buildKnowledgeDocumentPreviewTarget({
    knowledgeId: 'doc-1',
    title: 'untitled document',
  }), {
    knowledgeId: 'doc-1',
    fileName: 'untitled document',
    fileType: '',
  })
})

test('rejects incomplete knowledge preview targets', () => {
  assert.equal(buildKnowledgeDocumentPreviewTarget({ knowledgeId: '', title: 'paper.pdf' }), null)
  assert.equal(buildKnowledgeDocumentPreviewTarget({ knowledgeId: 'doc-1', title: '' }), null)
})
