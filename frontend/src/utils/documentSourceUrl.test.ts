import assert from 'node:assert/strict'
import test from 'node:test'
import { documentSourceUrl } from './documentSourceUrl.ts'

test('shows the original page for a synchronized file', () => {
  const source_url = 'https://confluence.example.test/wiki/viewpage.action?pageId=127'
  assert.equal(documentSourceUrl({ type: 'file', metadata: { source_url } }), source_url)
})

test('keeps the existing source for a URL import', () => {
  const source = 'https://example.test/import'
  assert.equal(documentSourceUrl({ type: 'url', source }), source)
})

test('ignores missing or unusable synchronized source metadata', () => {
  for (const source_url of [undefined, 42, '/wiki', 'ftp://example.test/page']) {
    assert.equal(documentSourceUrl({ type: 'file', metadata: { source_url } }), '')
  }
  assert.equal(documentSourceUrl(null), '')
})
