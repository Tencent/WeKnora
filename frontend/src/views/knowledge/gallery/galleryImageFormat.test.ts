import assert from 'node:assert/strict'
import test from 'node:test'
import { unsupportedGalleryFormat } from './galleryImageFormat.ts'

test('Office MIME types work with opaque resource URLs and MIME parameters', () => {
  for (const [mime, format] of [
    ['image/x-emf; charset=binary', 'EMF'], ['IMAGE/WMF', 'WMF'],
    ['application/vnd.ms-visio.drawing', 'Visio'], ['application/octet-stream', 'application/octet-stream'],
  ]) assert.equal(unsupportedGalleryFormat(mime, 'resource://opaque-id'), format)
})

test('direct image URLs fall back to their extension', () => {
  assert.equal(unsupportedGalleryFormat('', 'https://example.com/image.EMF?token=x'), 'EMF')
  assert.equal(unsupportedGalleryFormat('', 'local://diagram.vsdx'), 'Visio')
  assert.equal(unsupportedGalleryFormat('', 'local://diagram.wmf'), 'WMF')
})

test('converted images and ordinary load errors are not labeled unsupported', () => {
  assert.equal(unsupportedGalleryFormat('image/png', 'local://diagram.emf'), '')
  assert.equal(unsupportedGalleryFormat('image/jpeg', 'resource://opaque-id'), '')
  assert.equal(unsupportedGalleryFormat('', 'https://example.com/image.png'), '')
})
