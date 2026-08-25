import assert from 'node:assert/strict'
import test from 'node:test'
import { buildMultipartCompleteParts } from './upload'

test('multipart complete parts are emitted in ascending part number order', () => {
  const parts = buildMultipartCompleteParts(new Map([
    [3, '"etag-3"'],
    [1, 'etag-1'],
    [2, ' etag-2 '],
  ]), 3)

  assert.deepEqual(parts, [
    { part_number: 1, etag: 'etag-1' },
    { part_number: 2, etag: 'etag-2' },
    { part_number: 3, etag: 'etag-3' },
  ])
})

test('multipart complete parts reject missing parts before complete request', () => {
  assert.throws(
    () => buildMultipartCompleteParts(new Map([
      [1, 'etag-1'],
      [3, 'etag-3'],
    ]), 3),
    /分片上传未完成/,
  )
})

test('multipart complete parts reject duplicate part numbers', () => {
  assert.throws(
    () => buildMultipartCompleteParts([
      { part_number: 1, etag: 'etag-1' },
      { part_number: 1, etag: 'etag-1-retry' },
    ], 2),
    /重复上传完成/,
  )
})

test('multipart complete parts reject empty etags', () => {
  assert.throws(
    () => buildMultipartCompleteParts(new Map([
      [1, 'etag-1'],
      [2, ''],
    ]), 2),
    /缺少 ETag/,
  )
})

test('multipart complete parts reject out-of-range part numbers', () => {
  assert.throws(
    () => buildMultipartCompleteParts(new Map([
      [1, 'etag-1'],
      [4, 'etag-4'],
    ]), 2),
    /分片编号无效/,
  )
})
