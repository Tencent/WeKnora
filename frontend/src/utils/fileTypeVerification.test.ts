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

test('XMind uploads work before engine discovery and respect discovered capabilities', () => {
  for (const filename of ['architecture.xmind', 'ARCHITECTURE.XMIND']) {
    assert.equal(shouldRejectKnowledgeFileType(filename), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, []), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, new Set()), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, ['pdf', 'xmind']), false)
    assert.equal(shouldRejectKnowledgeFileType(filename, ['pdf']), true)
  }
})

// The backend accepts these Office/ODF/image variants (see
// internal/utils/import_filetypes.go), so the client fallback used before
// engine discovery must not reject them (#4598).
test('fallback whitelist covers every backend-accepted extension', () => {
  const accepted = [
    'xlsm', 'XLSM', 'docm', 'pptm', 'odt', 'ods', 'odp', 'rtf',
    'bmp', 'tiff', 'webp', 'gif', 'json', 'markdown',
  ]
  for (const ext of accepted) {
    assert.equal(
      shouldRejectKnowledgeFileType(`report.${ext}`),
      false,
      `${ext} should pass the fallback whitelist`,
    )
  }
  assert.equal(shouldRejectKnowledgeFileType('payload.exe'), true)
})
