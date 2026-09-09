import assert from 'node:assert/strict'
import test from 'node:test'

import {
  isSafePreviewImageHref,
  renderDocumentPreviewMarkdown,
} from './documentPreviewMarkdown.ts'
import { applyDocumentPreviewImageAttributes } from './security.ts'

test('preview Markdown keeps native lazy image attributes', () => {
  const html = renderDocumentPreviewMarkdown('![test](https://example.com/test.png)', value => value)

  assert.match(html, /src="https:\/\/example.com\/test.png"/)
  assert.match(html, /loading="lazy"/)
  assert.match(html, /decoding="async"/)
  assert.match(html, /fetchpriority="low"/)
})

test('preview Markdown keeps data URI images used by document parsers', () => {
  const html = renderDocumentPreviewMarkdown(
    '![chart](data:image/png;base64,AAAA)',
    value => value,
  )

  assert.match(html, /src="data:image\/png;base64,AAAA"/)
  assert.match(html, /loading="lazy"/)
})

test('preview Markdown keeps relative and protocol-relative images', () => {
  const relative = renderDocumentPreviewMarkdown('![rel](images/a.png)', value => value)
  const protocolRelative = renderDocumentPreviewMarkdown(
    '![cdn](//cdn.example.com/a.png)',
    value => value,
  )

  assert.match(relative, /src="images\/a.png"/)
  assert.match(protocolRelative, /src="\/\/cdn.example.com\/a.png"/)
})

test('preview Markdown drops javascript image URLs', () => {
  const html = renderDocumentPreviewMarkdown('![x](javascript:alert(1))', value => value)

  assert.doesNotMatch(html, /javascript:/i)
  assert.doesNotMatch(html, /<img/i)
})

test('isSafePreviewImageHref matches the shared DOMPurify image URI policy', () => {
  assert.equal(isSafePreviewImageHref('https://example.com/a.png'), true)
  assert.equal(isSafePreviewImageHref('data:image/png;base64,AAAA'), true)
  assert.equal(isSafePreviewImageHref('resource://AbCdEfGhIjKlMnOpQrStUv'), true)
  assert.equal(isSafePreviewImageHref('/files/a.png'), true)
  assert.equal(isSafePreviewImageHref('images/a.png'), true)
  assert.equal(isSafePreviewImageHref('//cdn.example.com/a.png'), true)
  assert.equal(isSafePreviewImageHref('javascript:alert(1)'), false)
  assert.equal(isSafePreviewImageHref('data:text/html,<script>alert(1)</script>'), false)
  assert.equal(isSafePreviewImageHref('#fragment'), false)
})

test('preview Markdown keeps the marked-katex extension when using an image renderer', () => {
  const html = renderDocumentPreviewMarkdown('公式 $E = mc^2$', value => value)

  assert.match(html, /katex/)
})

test('preview Markdown skips highlightAuto on huge unlabeled fences', () => {
  const source = 'a'.repeat(40_000)
  const html = renderDocumentPreviewMarkdown('```\n' + source + '\n```', value => value)

  assert.match(html, /<pre><code class="hljs">/)
  assert.equal(html.includes(`>${source}<`), true)
  assert.doesNotMatch(html, /hljs-/)
})

test('raw HTML images cannot override preview loading attributes', () => {
  const attributes = new Map([
    ['alt', 'score > 90'],
    ['title', 'A > B'],
    ['loading', 'eager'],
    ['fetchpriority', 'high'],
  ])
  const image = {
    tagName: 'IMG',
    setAttribute: (name: string, value: string) => attributes.set(name, value),
  }
  applyDocumentPreviewImageAttributes(image as unknown as Node)
  assert.equal(attributes.get('alt'), 'score > 90')
  assert.equal(attributes.get('title'), 'A > B')
  assert.equal(attributes.get('loading'), 'lazy')
  assert.equal(attributes.get('decoding'), 'async')
  assert.equal(attributes.get('fetchpriority'), 'low')
})
