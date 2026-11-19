import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getHighlightLang,
  getPreviewMimeType,
  isKnownPreviewableExt,
  isValidUTF8,
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
  // Legacy OLE2 formats are not parseable by the OOXML previewers and must
  // degrade to the unsupported state instead of failing with a zip error.
  assert.equal(resolvePreviewKind('doc'), 'unsupported')
  assert.equal(resolvePreviewKind('ppt'), 'unsupported')
  // SheetJS renders legacy .xls fine, so it stays previewable.
  assert.equal(resolvePreviewKind('xls'), 'excel')
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

test('isValidUTF8 rejects invalid scalar encodings so GBK CSV can fall back', () => {
  // GBK for "蜡,1": accepting C0 AF as UTF-8 produces replacement characters.
  const csv = Uint8Array.from([0xC0, 0xAF, 0x2C, 0x31])
  assert.equal(isValidUTF8(csv), false)
  assert.equal(new TextDecoder('gbk').decode(csv), '蜡,1')
  for (const bytes of [
    [0xC1, 0xBF], [0xE0, 0x80, 0xAF], [0xED, 0xA0, 0x80],
    [0xF0, 0x80, 0x80, 0xAF], [0xF4, 0x90, 0x80, 0x80],
    [0xF5, 0x80, 0x80, 0x80], [0x80],
  ]) {
    assert.equal(isValidUTF8(Uint8Array.from(bytes)), false, String(bytes))
    assert.equal(sniffPreview(Uint8Array.from(bytes)).kind, 'unsupported', String(bytes))
  }
})

test('isValidUTF8 only tolerates valid incomplete prefixes when requested', () => {
  for (const bytes of [[0xC2], [0xE4, 0xB8], [0xF0, 0x9F, 0x98]]) {
    assert.equal(isValidUTF8(Uint8Array.from(bytes)), false)
    assert.equal(isValidUTF8(Uint8Array.from(bytes), true), true)
  }
  for (const bytes of [[0xC0], [0xF5], [0xE4, 0x41], [0xE0, 0x80], [0xED, 0xA0], [0xF4, 0x90]]) {
    assert.equal(isValidUTF8(Uint8Array.from(bytes), true), false, String(bytes))
  }
})

test('isValidUTF8 accepts valid Unicode including scalar boundaries', () => {
  for (const text of ['', 'ASCII', '中文😀', '\u0080\u07FF\u0800\uD7FF\uE000\uFFFF', '\u{10000}\u{10FFFF}']) {
    const bytes = new TextEncoder().encode(text)
    assert.equal(isValidUTF8(bytes), true)
    assert.equal(isValidUTF8(bytes, true), true)
  }
})

test('pretty-print helpers only rewrite canonical JSON documents', () => {
  assert.equal(shouldPrettyPrintJson('json'), true)
  assert.equal(shouldPrettyPrintJson('jsonl'), false)
  assert.equal(prettyPrintJson('{"a":1}'), '{\n  "a": 1\n}')
  assert.equal(prettyPrintJson('not json'), 'not json')
})

test('JSON preview preserves numeric literals instead of rounding or replacing them', () => {
  for (const number of [
    '9007199254740993', '-9007199254740993', '9223372036854775807',
    '0.1234567890123456789012345', '1e400', '1e-400', '-0', '1.2300',
  ]) {
    assert.equal(prettyPrintJson(`{"value":${number}}`), `{\n  "value": ${number}\n}`, number)
  }
})

test('JSON preview formats nested containers without rewriting string contents', () => {
  // Strings containing JSON punctuation and escaped quotes are indivisible tokens.
  const ordinary = '{"items":[1,{"text":"a, b: [c] {d}","empty":[]}],"object":{},"ok":true,"nil":null}'
  assert.equal(prettyPrintJson(ordinary), JSON.stringify(JSON.parse(ordinary), null, 2))
  const escaped = String.raw`{"text":"comma, colon: quote\" slash\\ newline\n","id":9007199254740993}`
  assert.equal(prettyPrintJson(escaped), String.raw`{
  "text": "comma, colon: quote\" slash\\ newline\n",
  "id": 9007199254740993
}`)
})

test('JSON preview preserves top-level values and leaves invalid JSON unchanged', () => {
  for (const value of ['9007199254740993', '1e400', '-0', 'null', 'true', '"text"', '[]', '{}']) {
    assert.equal(prettyPrintJson(`  ${value}\n`), value)
  }
  for (const value of ['  {"id":01}', '{"id":1,}', '{"id":NaN}', '{"unfinished":', '  \n']) {
    assert.equal(prettyPrintJson(value), value)
  }
})

test('mime and highlight maps cover skill-generated html/csv', () => {
  assert.equal(getPreviewMimeType('html'), 'text/html')
  assert.equal(getPreviewMimeType('csv'), 'text/csv')
  assert.equal(getHighlightLang('html'), 'xml')
  assert.equal(getHighlightLang('ts'), 'typescript')
})

function ftyp(major: string, compatible: string[] = []) {
  const brands = major + String.fromCharCode(0, 0, 0, 0) + compatible.join('')
  const bytes = new Uint8Array(8 + brands.length)
  new DataView(bytes.buffer).setUint32(0, bytes.length)
  bytes.set(new TextEncoder().encode('ftyp'), 4)
  bytes.set(new TextEncoder().encode(brands), 8)
  return bytes
}

test('sniffPreview distinguishes images and audio in ISO base media files', () => {
  for (const brand of ['avif', 'avis']) {
    assert.deepEqual(sniffPreview(ftyp(brand)), { kind: 'image', ext: 'avif' })
  }
  for (const brand of ['heic', 'heix', 'hevc', 'hevx']) {
    assert.deepEqual(sniffPreview(ftyp(brand)), { kind: 'image', ext: 'heic' })
  }
  for (const brand of ['M4A ', 'M4B ']) {
    assert.deepEqual(sniffPreview(ftyp(brand)), { kind: 'audio', ext: 'm4a' })
  }
})

test('sniffPreview checks compatible image brands when the major brand is generic', () => {
  assert.deepEqual(sniffPreview(ftyp('mif1', ['miaf', 'avif'])), { kind: 'image', ext: 'avif' })
  assert.deepEqual(sniffPreview(ftyp('mif1', ['heic'])), { kind: 'image', ext: 'heic' })
})

test('sniffPreview retains video fallback and confines brands to the ftyp box', () => {
  assert.deepEqual(sniffPreview(ftyp('isom', ['mp42'])), { kind: 'video', ext: 'mp4' })
  const video = ftyp('isom')
  const withTrailingBytes = new Uint8Array(video.length + 4)
  withTrailingBytes.set(video)
  withTrailingBytes.set(new TextEncoder().encode('avif'), video.length)
  assert.deepEqual(sniffPreview(withTrailingBytes), { kind: 'video', ext: 'mp4' })
})
