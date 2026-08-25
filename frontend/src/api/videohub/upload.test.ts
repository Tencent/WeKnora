import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildMultipartCompleteParts,
  getMultipartPartSizes,
  PART_SIZE,
  UploadCancelledError,
  uploadPartWithRetry,
} from './upload'

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

test('multipart part retry records attempts and succeeds on the third attempt', async () => {
  const attempts: number[] = []
  const failures: number[] = []
  const result = await uploadPartWithRetry(
    2,
    3,
    async attempt => {
      attempts.push(attempt)
      if (attempt < 3) throw new Error(`failed-${attempt}`)
      return 'etag-2'
    },
    {
      maxRetries: 3,
      retryDelayMs: 0,
      sleep: async () => {},
      onAttemptFailed: attempt => failures.push(attempt),
    },
  )

  assert.equal(result, 'etag-2')
  assert.deepEqual(attempts, [1, 2, 3])
  assert.deepEqual(failures, [1, 2])
})

test('multipart part retry stops immediately when cancelled', async () => {
  const cancel = { cancelled: true }
  await assert.rejects(
    () => uploadPartWithRetry(1, 1, async () => 'etag-1', { cancel }),
    error => error instanceof UploadCancelledError,
  )
})

for (const fileSizeMB of [100, 200, 500]) {
  test(`local ${fileSizeMB}MB multipart regression retries every part twice`, async () => {
    const partSizes = getMultipartPartSizes(fileSizeMB * 1024 * 1024)
    assert.equal(partSizes.length, fileSizeMB / 5)
    assert.equal(partSizes.at(-1), PART_SIZE)

    let totalAttempts = 0
    for (const [index, partSize] of partSizes.entries()) {
      let attempts = 0
      const result = await uploadPartWithRetry(
        index + 1,
        partSizes.length,
        async attempt => {
          attempts = attempt
          totalAttempts++
          if (attempt < 3) throw new Error(`synthetic local failure: part=${index + 1}`)
          return partSize
        },
        {
          maxRetries: 3,
          retryDelayMs: 0,
          sleep: async () => {},
        },
      )
      assert.equal(result, partSize)
      assert.equal(attempts, 3)
    }
    assert.equal(totalAttempts, partSizes.length * 3)
  })
}

test('local 100MB multipart regression succeeds in ten consecutive runs', async () => {
  const partSizes = getMultipartPartSizes(100 * 1024 * 1024)
  for (let run = 0; run < 10; run++) {
    for (const [index, partSize] of partSizes.entries()) {
      const result = await uploadPartWithRetry(
        index + 1,
        partSizes.length,
        async attempt => {
          if (attempt < 3) throw new Error(`synthetic local failure: run=${run}, part=${index + 1}`)
          return partSize
        },
        { maxRetries: 3, retryDelayMs: 0, sleep: async () => {} },
      )
      assert.equal(result, partSize)
    }
  }
})
