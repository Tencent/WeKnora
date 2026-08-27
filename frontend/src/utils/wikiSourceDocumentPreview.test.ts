import assert from 'node:assert/strict'
import test from 'node:test'

import { isWikiSourceDocumentPreviewEnabled } from './wikiSourceDocumentPreview.ts'

test('enables Wiki source preview only with explicit server authorization', () => {
  assert.equal(isWikiSourceDocumentPreviewEnabled({
    knowledge_id: 'doc-1',
    knowledge_base_id: 'kb-1',
    preview_enabled: true,
  }), true)
})

test('document identity alone cannot recover disabled Wiki source preview', () => {
  assert.equal(isWikiSourceDocumentPreviewEnabled({
    knowledge_id: 'doc-1',
    knowledge_base_id: 'kb-1',
  }), false)
  assert.equal(isWikiSourceDocumentPreviewEnabled({
    knowledge_id: 'doc-1',
    knowledge_base_id: 'kb-1',
    preview_enabled: false,
  }), false)
})

test('authorization without complete document identity is rejected', () => {
  assert.equal(isWikiSourceDocumentPreviewEnabled({
    knowledge_id: 'doc-1',
    preview_enabled: true,
  }), false)
  assert.equal(isWikiSourceDocumentPreviewEnabled({
    knowledge_base_id: 'kb-1',
    preview_enabled: true,
  }), false)
})
