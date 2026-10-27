import assert from 'node:assert/strict'
import test from 'node:test'

import { documentPreviewDomPurifyConfig, protectProviderImageSrcInHTML } from './security.ts'

test('protectProviderImageSrcInHTML uses a placeholder src for provider images', () => {
  const html = '<p><img alt="preview" src="local://10000/exports/a.jpg"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(sanitized, /data-protected-src="local:\/\/10000\/exports\/a\.jpg"/)
})

test('protectProviderImageSrcInHTML uses a placeholder src for storage-backend images', () => {
  const html =
    '<p><img alt="preview" src="storage://c0d93536-702c-4977-aa5e-fe670073c3cb/local://10000/exports/a.png"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(
    sanitized,
    /data-protected-src="storage:\/\/c0d93536-702c-4977-aa5e-fe670073c3cb\/local:\/\/10000\/exports\/a\.png"/,
  )
})

test('protectProviderImageSrcInHTML uses a placeholder src for resource references', () => {
  const html = '<p><img alt="preview" src="resource://AbCdEfGhIjKlMnOpQrStUv"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(
    sanitized,
    /data-protected-src="resource:\/\/AbCdEfGhIjKlMnOpQrStUv"/,
  )
})

test('document preview sanitizer keeps target on links alongside image attributes', () => {
  // USE_PROFILES replaces ALLOWED_ATTR, so target must come from ADD_ATTR or
  // the hook-added target="_blank" is stripped again.
  for (const attr of ['target', 'loading', 'decoding', 'fetchpriority']) {
    assert.ok(documentPreviewDomPurifyConfig.ADD_ATTR.includes(attr), attr)
  }
})
