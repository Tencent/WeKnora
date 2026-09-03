import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getHighlightLang,
  getPreviewMimeType,
  isKnownPreviewableExt,
  prettyPrintJson,
  resolveFilePreviewExt,
  resolvePreviewKind,
  shouldPrettyPrintJson,
  sniffPreview,
} from './filePreview.ts'

test('resolveFilePreviewExt prefers explicit type and strips a leading dot', () => {
  assert.equal(resolveFilePreviewExt('chart.html', '.HTML'), 'html')
  assert.equal(resolveFilePreviewExt('kb_stats.csv', ''), 'csv')
  assert.equal(resolveFilePreviewExt('noext', ''), '')
})

test('resolvePreviewKind covers office, tables, html, media, and code', () => {
  assert.equal(resolvePreviewKind('html'), 'html')
  assert.equal(resolvePreviewKind('htm'), 'html')
  assert.equal(resolvePreviewKind('csv'), 'excel')
  assert.equal(resolvePreviewKind('tsv'), 'excel')
  assert.equal(resolvePreviewKind('xlsx'), 'excel')
  assert.equal(resolvePreviewKind('pdf'), 'pdf')
  assert.equal(resolvePreviewKind('docx'), 'docx')
  assert.equal(resolvePreviewKind('pptx'), 'pptx')
  assert.equal(resolvePreviewKind('png'), 'image')
  assert.equal(resolvePreviewKind('svg'), 'image')
  assert.equal(resolvePreviewKind('md'), 'markdown')
  assert.equal(resolvePreviewKind('mp4'), 'video')
  assert.equal(resolvePreviewKind('mp3'), 'audio')
  assert.equal(resolvePreviewKind('mmd'), 'mermaid')
  assert.equal(resolvePreviewKind('vue'), 'text')
  assert.equal(resolvePreviewKind('ipynb'), 'text')
  assert.equal(resolvePreviewKind('exe'), 'unsupported')
  assert.equal(isKnownPreviewableExt('html'), true)
  assert.equal(isKnownPreviewableExt('bin'), false)
})

test('sniffPreview detects html, svg, pdf, png, and mermaid from bytes', () => {
  assert.equal(sniffPreview(new TextEncoder().encode('<!DOCTYPE html><html><body>ok</body></html>')).kind, 'html')
  assert.equal(sniffPreview(new TextEncoder().encode('<div id="chart"></div>')).kind, 'html')
  assert.equal(sniffPreview(new TextEncoder().encode('<table><tr><td>1</td></tr></table>')).kind, 'html')
  assert.equal(sniffPreview(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"></svg>')).kind, 'image')
  assert.equal(sniffPreview(new TextEncoder().encode('%PDF-1.7\n')).kind, 'pdf')
  assert.equal(sniffPreview(new TextEncoder().encode('flowchart TD\n  A-->B')).kind, 'mermaid')
  assert.equal(sniffPreview(new TextEncoder().encode('{"ok": true}')).kind, 'text')

  const png = Uint8Array.from([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0])
  assert.equal(sniffPreview(png).kind, 'image')
  assert.equal(sniffPreview(png).ext, 'png')
})

test('sniffPreview rejects NUL-containing binaries', () => {
  assert.equal(sniffPreview(Uint8Array.from([0x00, 0x01, 0x02, 0x03])).kind, 'unsupported')
})

test('pretty-print helpers only rewrite canonical JSON documents', () => {
  assert.equal(shouldPrettyPrintJson('json'), true)
  assert.equal(shouldPrettyPrintJson('jsonl'), false)
  assert.equal(prettyPrintJson('{"a":1}'), '{\n  "a": 1\n}')
  assert.equal(prettyPrintJson('not json'), 'not json')
})

test('mime and highlight maps cover skill-generated html/csv', () => {
  assert.equal(getPreviewMimeType('html'), 'text/html')
  assert.equal(getPreviewMimeType('csv'), 'text/csv')
  assert.equal(getHighlightLang('html'), 'xml')
  assert.equal(getHighlightLang('ts'), 'typescript')
})
