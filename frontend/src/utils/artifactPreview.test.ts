import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveArtifactPreview } from './artifactPreview'

test('kind controls presentation while keeping the actual file format renderer', () => {
  const result = resolveArtifactPreview({ kind: 'presentation', file_name: 'slides.html', file_type: 'html' }, true)
  assert.deepEqual(result, { kind: 'presentation', ext: 'html', icon: 'file-powerpoint' })
  assert.equal(resolveArtifactPreview({ kind: 'presentation', file_name: 'slides.pptx' }).ext, 'pptx')
  assert.equal(resolveArtifactPreview({ kind: 'spreadsheet', file_name: 'table.csv' }).ext, 'csv')
  assert.equal(resolveArtifactPreview({ kind: 'web_page', file_name: 'slides.pptx' }, true).ext, 'pptx')
  assert.equal(resolveArtifactPreview({ kind: 'presentation', file_name: 'table.xlsx' }, true).ext, 'xlsx')
})

test('legacy payloads fall back to extensions and unknown categories do not break preview', () => {
  assert.equal(resolveArtifactPreview({ file_name: 'slides.pptx' }).kind, 'presentation')
  assert.equal(resolveArtifactPreview({ file_name: 'report.html' }).kind, 'web_page')
  assert.equal(resolveArtifactPreview({ file_name: 'table.csv' }).kind, 'spreadsheet')
  assert.equal(resolveArtifactPreview({ file_name: 'data.txt', kind: 'future' as never }).kind, 'text')
  assert.equal(resolveArtifactPreview({ file_name: 'chart.svg', file_type: 'image/svg+xml' }).ext, 'svg')
})

test('category hints cannot opt an ordinary artifact into HTML script rendering', () => {
  const item = { kind: 'web_page' as const, file_name: 'output.bin' }
  assert.equal(resolveArtifactPreview(item).ext, 'bin')
  assert.equal(resolveArtifactPreview(item, true).ext, 'html')
  assert.equal(resolveArtifactPreview({ kind: 'presentation', file_name: 'slides.html' }, true).ext, 'html')
})
