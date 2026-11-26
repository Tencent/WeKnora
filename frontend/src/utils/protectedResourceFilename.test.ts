import assert from 'node:assert/strict'
import test from 'node:test'

import { responseFileName } from './protectedResource.ts'

for (const language of ['en', 'zh-CN', '']) {
  test(`extended UTF-8 download filename supports language '${language}'`, () => {
    const header = `attachment; filename="fallback.pptx"; filename*=UTF-8'${language}'%E6%8A%A5%E5%91%8A.pptx`
    assert.equal(responseFileName(header, 'resource://opaque-id'), '报告.pptx')
  })
}

test('extended filenames tolerate parameter whitespace and case', () => {
  assert.equal(responseFileName("inline; FILENAME* = utf-8'en'%E6%8A%A5%E5%91%8A.pdf  ", ''), '报告.pdf')
})

test('malformed extended filename retains the ordinary filename', () => {
  assert.equal(responseFileName("attachment; filename=report.pdf; filename*=UTF-8'en'%ZZ", ''), 'report.pdf')
})

test('extended filename still strips unsafe path components and control bytes', () => {
  assert.equal(responseFileName("attachment; filename*=UTF-8'en'..%2F..%2Freport%00.pdf", ''), 'report.pdf')
})

test('source filename remains the fallback without a disposition', () => {
  assert.equal(responseFileName(null, 'local://1/exports/report.pdf'), 'report.pdf')
})

test('ordinary quoted filenames preserve escaped quotes and embedded semicolons', () => {
  const header = String.raw`attachment; filename="Quarterly \"draft\"; 2026.pdf"`
  assert.equal(responseFileName(header, ''), 'Quarterly "draft"; 2026.pdf')
})

test('filename parameters inside another quoted parameter are ignored', () => {
  const header = `attachment; note="hint; filename*=UTF-8''wrong.pdf"; filename = "report.pdf"`
  assert.equal(responseFileName(header, 'local://1/fallback.pdf'), 'report.pdf')
})
