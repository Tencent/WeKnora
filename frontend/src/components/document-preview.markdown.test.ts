import assert from 'node:assert/strict'
import test from 'node:test'

import { renderDocumentPreviewMarkdown } from '../utils/documentPreviewMarkdown.ts'
import { applyDocumentPreviewImageAttributes } from '../utils/security.ts'

test('preview Markdown keeps native lazy image attributes', () => {
  const html = renderDocumentPreviewMarkdown('![test](https://example.com/test.png)', value => value)

  assert.match(html, /loading="lazy"/)
  assert.match(html, /decoding="async"/)
  assert.match(html, /fetchpriority="low"/)
})

test('preview Markdown keeps the marked-katex extension when using an image renderer', () => {
  const html = renderDocumentPreviewMarkdown('公式 $E = mc^2$', value => value)

  assert.match(html, /katex/)
})

test('raw HTML images cannot override preview loading attributes', () => {
  const attributes = new Map([['alt', 'score > 90'], ['title', 'A > B'], ['loading', 'eager'], ['fetchpriority', 'high']])
  const image = { tagName: 'IMG', setAttribute: (name: string, value: string) => attributes.set(name, value) }
  applyDocumentPreviewImageAttributes(image as unknown as Node)
  assert.equal(attributes.get('alt'), 'score > 90')
  assert.equal(attributes.get('title'), 'A > B')
  assert.equal(attributes.get('loading'), 'lazy')
  assert.equal(attributes.get('decoding'), 'async')
  assert.equal(attributes.get('fetchpriority'), 'low')
})
