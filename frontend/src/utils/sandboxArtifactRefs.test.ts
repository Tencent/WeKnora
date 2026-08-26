import assert from 'node:assert/strict'
import test from 'node:test'

import {
  isArtifactRefHref,
  normalizeSandboxArtifactRefs,
  renderArtifactReference,
  resolveArtifactRef,
  type ArtifactRefMeta,
} from './sandboxArtifactRefs.ts'

const labels = { previewHint: '点击预览', missingHint: '文件不可用' }

const artifacts: ArtifactRefMeta[] = [
  { index: 0, file_name: '市场画像评分_e7edba.html', file_type: 'text/html' },
  { index: 1, file_name: 'trend.png', file_type: 'image/png' },
  { index: 2, file_name: 'diagram.svg', file_type: 'image/svg+xml' },
  { index: 3, file_name: '腾讯控股(00700) 成交量_838ccc.html', file_type: 'text/html' },
]

test('isArtifactRefHref only matches sandbox artifact references', () => {
  assert.equal(isArtifactRefHref('artifact://0'), true)
  assert.equal(isArtifactRefHref('sandbox:trend.png'), true)
  assert.equal(isArtifactRefHref('sandbox://trend.png'), true)
  assert.equal(isArtifactRefHref('https://example.com/trend.png'), false)
  assert.equal(isArtifactRefHref('resource://tenant/1/trend.png'), false)
  assert.equal(isArtifactRefHref('trend.png'), false)
  assert.equal(isArtifactRefHref(''), false)
})

test('resolveArtifactRef resolves both the index and the name form', () => {
  assert.equal(resolveArtifactRef('artifact://1', artifacts)?.file_name, 'trend.png')
  assert.equal(resolveArtifactRef('sandbox:trend.png', artifacts)?.index, 1)
  assert.equal(
    resolveArtifactRef('sandbox:/workspace/output/trend.png', artifacts)?.index,
    1,
  )
  assert.equal(
    resolveArtifactRef('sandbox:%E5%B8%82%E5%9C%BA%E7%94%BB%E5%83%8F%E8%AF%84%E5%88%86_e7edba.html', artifacts)?.index,
    0,
  )
  assert.equal(resolveArtifactRef('artifact://9', artifacts), null)
  assert.equal(resolveArtifactRef('sandbox:missing.csv', artifacts), null)
})

test('renderArtifactReference leaves ordinary images to the default renderer', () => {
  assert.equal(
    renderArtifactReference({ href: 'https://example.com/a.png', artifacts, labels }),
    null,
  )
  assert.equal(
    renderArtifactReference({ href: 'resource://tenant/1/a.png', artifacts, labels }),
    null,
  )
})

test('renderArtifactReference renders image artifacts inline', () => {
  const html = renderArtifactReference({
    href: 'artifact://1',
    alt: '走势',
    artifacts,
    labels,
  })
  assert.ok(html?.includes('class="markdown-image artifact-ref-image"'))
  assert.ok(html?.includes('data-artifact-index="1"'))
  assert.ok(html?.includes('data-img-loading="1"'))
  assert.ok(html?.includes('alt="走势"'))
})

test('renderArtifactReference renders non-image artifacts as a clickable card', () => {
  const html = renderArtifactReference({
    href: 'artifact://0',
    alt: '市场画像评分',
    artifacts,
    labels,
  })
  assert.ok(html?.includes('class="artifact-ref-card"'))
  assert.ok(html?.includes('data-artifact-index="0"'))
  assert.ok(html?.includes('role="button"'))
  assert.ok(html?.includes('市场画像评分_e7edba.html'))
  assert.ok(html?.includes('点击预览'))
  assert.ok(!html?.includes('<img'))
})

test('svg artifacts go through the sandboxed preview rather than inline rendering', () => {
  const html = renderArtifactReference({ href: 'artifact://2', artifacts, labels })
  assert.ok(html?.includes('artifact-ref-card'))
  assert.ok(!html?.includes('artifact-ref-image'))
})

test('while streaming an unresolved reference shows the image skeleton', () => {
  // Artifacts are only collected once the turn ends, so mid-stream every
  // reference is unresolved; a card there would flash a half-written name.
  const html = renderArtifactReference({
    href: 'sandbox:市场画像评分_e7edba.html',
    artifacts: [],
    labels,
    streaming: true,
  })
  assert.ok(html?.includes('streaming-image-loading'))
  assert.ok(!html?.includes('artifact-ref-card'))
})

test('after the turn ends an unresolved reference says so instead of hanging', () => {
  const html = renderArtifactReference({
    href: 'sandbox:市场画像评分_e7edba.html',
    alt: '市场画像评分',
    artifacts: [],
    labels,
  })
  assert.ok(html?.includes('artifact-ref-card--pending'))
  assert.ok(html?.includes('文件不可用'))
  assert.ok(!html?.includes('data-artifact-index'))
  assert.ok(!html?.includes('role="button"'))
})

test('file names with spaces and parentheses resolve end to end', () => {
  const raw = '![成交量](sandbox:腾讯控股(00700) 成交量_838ccc.html)'
  const normalized = normalizeSandboxArtifactRefs(raw)

  // marked would split the raw destination at the first space; after
  // normalization it is a single token.
  const href = normalized.slice(normalized.indexOf('](') + 2, normalized.lastIndexOf(')'))
  assert.ok(!href.includes(' '))
  assert.equal(resolveArtifactRef(href, artifacts)?.index, 3)

  const html = renderArtifactReference({ href, alt: '成交量', artifacts, labels })
  assert.ok(html?.includes('腾讯控股(00700) 成交量_838ccc.html'))
  assert.ok(html?.includes('data-artifact-index="3"'))
})

test('normalizeSandboxArtifactRefs leaves everything else alone', () => {
  const untouched = [
    '![远程](https://example.com/a(b).png)',
    '![资源](resource://tenant/1/a.png)',
    '腾讯控股(00700) 的成交量见下图。',
    '写成 `![图](sandbox:a b.html)` 即可',
    '```\n![图](sandbox:a b.html)\n```',
  ]
  for (const markdown of untouched) {
    assert.equal(normalizeSandboxArtifactRefs(markdown), markdown)
  }
})

test('normalizeSandboxArtifactRefs preserves a title and an unterminated tail', () => {
  assert.equal(
    normalizeSandboxArtifactRefs('![图](sandbox:a b.html "说明")'),
    '![图](sandbox:a%20b.html "说明")',
  )
  // Mid-stream the closing paren has not arrived yet; leave the tail for the
  // streaming placeholder guard rather than guessing where it ends.
  assert.equal(
    normalizeSandboxArtifactRefs('![图](sandbox:腾讯控股(00700'),
    '![图](sandbox:腾讯控股(00700',
  )
})

test('artifact names are HTML-escaped', () => {
  const html = renderArtifactReference({
    href: 'artifact://0',
    artifacts: [{ index: 0, file_name: '<img src=x onerror=alert(1)>.csv' }],
    labels,
  })
  assert.ok(!html?.includes('<img src=x'))
  assert.ok(html?.includes('&lt;img'))
})
