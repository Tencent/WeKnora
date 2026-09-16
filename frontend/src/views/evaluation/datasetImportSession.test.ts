import assert from 'node:assert/strict'
import test from 'node:test'
import { createDatasetImportSession } from './datasetImportSession'
import { DATASET_TEMPLATE } from './datasetValidation'
import type { DatasetImportRequest, DatasetImportResult } from '../../api/evaluation/datasets'

const result = { dataset: { id: 'dataset-1' }, version: { id: 'version-1' }, replayed: false } as DatasetImportResult
const input = () => ({ name: 'Sample', description: '', content: structuredClone(DATASET_TEMPLATE) })
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
test('double clicking shares one request and a completed import does not post again', async () => {
  const response = deferred<DatasetImportResult>(); const requests: DatasetImportRequest[] = []
  const session = createDatasetImportSession(request => { requests.push(request); return response.promise }, () => 'one-uuid')
  const first = session.submit(input()), second = session.submit(input())
  assert.equal(first, second); assert.equal(requests.length, 1)
  response.resolve(result); assert.equal(await first, result)
  assert.equal(await session.submit(input()), result); assert.equal(requests.length, 1)
})
test('a failed import retries the exact UUID and original content, even if caller mutates draft', async () => {
  const requests: DatasetImportRequest[] = []; let attempt = 0
  const session = createDatasetImportSession(async request => { requests.push(structuredClone(request)); if (++attempt === 1) throw new Error('network lost'); return { ...result, replayed: true } }, () => 'stable-uuid')
  const draft = input()
  await assert.rejects(session.submit(draft), /network lost/)
  draft.name = 'Changed'; draft.content.relevance[0]!.grade = 0
  const saved = await session.submit(draft)
  assert.equal(saved?.replayed, true); assert.deepEqual(requests[1], requests[0])
})
test('a workspace switch discards an earlier response and starts a new request identity', async () => {
  const old = deferred<DatasetImportResult>(); let calls = 0
  const session = createDatasetImportSession(() => ++calls === 1 ? old.promise : Promise.resolve(result), () => `uuid-${calls + 1}`)
  const pending = session.submit(input()); session.reset()
  assert.equal(await session.submit(input()), result)
  old.resolve(result); assert.equal(await pending, undefined)
})
