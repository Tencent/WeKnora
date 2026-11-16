import assert from 'node:assert/strict'
import test from 'node:test'

import { shouldRejectKnowledgeFileType } from './fileTypeVerification.ts'

test('shouldRejectKnowledgeFileType accepts HTML in the fallback whitelist', () => {
  assert.equal(shouldRejectKnowledgeFileType('page.html'), false)
  assert.equal(shouldRejectKnowledgeFileType('legacy.HTM'), false)
  assert.equal(shouldRejectKnowledgeFileType('payload.exe'), true)
})

test('shouldRejectKnowledgeFileType preserves dynamic whitelist behavior', () => {
  assert.equal(shouldRejectKnowledgeFileType('custom.xyz', ['xyz']), false)
  assert.equal(shouldRejectKnowledgeFileType('page.html', ['pdf']), true)
  assert.equal(shouldRejectKnowledgeFileType('page.html', []), false)
})

test('filenames without an extension are rejected even if they match a supported type', () => {
  assert.equal(shouldRejectKnowledgeFileType('pdf'), true)
  assert.equal(shouldRejectKnowledgeFileType('xyz', ['xyz']), true)
  assert.equal(shouldRejectKnowledgeFileType('report.PDF'), false)
})

test('XMind uploads work before engine discovery and respect discovered capabilities', () => {
  for (const filename of ['architecture.xmind', 'ARCHITECTURE.XMIND']) {
    assert.equal(shouldRejectKnowledgeFileType(filename), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, []), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, new Set()), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, ['pdf', 'xmind']), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, ['pdf']), true)
  }
})
