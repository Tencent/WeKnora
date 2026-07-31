import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildKnowledgeFolderUploadPlan,
  chunkKnowledgeFolderEnsurePaths,
  getKnowledgeFolderSegments,
  mapKnowledgeFolderUploadTargets,
} from './knowledgeFolderUpload.ts'

function relativeFile(name: string, webkitRelativePath = ''): File {
  return {
    name,
    size: 1,
    webkitRelativePath,
  } as unknown as File
}

test('gets folder segments from a path with a selected top-level folder', () => {
  assert.deepEqual(getKnowledgeFolderSegments('Project/docs/api.md'), ['docs'])
})

test('gets folder segments from a path without a selected top-level folder', () => {
  assert.deepEqual(getKnowledgeFolderSegments('docs/api.md'), ['docs'])
})

test('returns no folder segments for a single file path', () => {
  assert.deepEqual(getKnowledgeFolderSegments('api.md'), [])
})

test('returns no folder segments for an empty path', () => {
  assert.deepEqual(getKnowledgeFolderSegments(''), [])
})

test('gets all nested folder segments from a multi-level path', () => {
  assert.deepEqual(getKnowledgeFolderSegments('Project/docs/reference/api.md'), [
    'docs',
    'reference',
  ])
})

test('builds an ensure path when the selected top-level folder is absent', () => {
  const api = relativeFile('api.md', 'docs/api.md')

  assert.deepEqual(buildKnowledgeFolderUploadPlan([api]).paths, [
    { client_key: 'p0', segments: ['docs'] },
  ])
})

test('strips the selected top-level folder and filename from relative paths', () => {
  const api = relativeFile('api.md', 'Project/docs/api.md')
  const guide = relativeFile('guide.md', 'Project/docs/guide.md')
  const nested = relativeFile('auth.md', 'Project/docs/reference/auth.md')
  const root = relativeFile('README.md', 'README.md')
  const ordinary = relativeFile('notes.md')

  const plan = buildKnowledgeFolderUploadPlan([api, guide, nested, root, ordinary])

  assert.deepEqual(plan.paths, [
    { client_key: 'p0', segments: ['docs'] },
    { client_key: 'p1', segments: ['docs', 'reference'] },
  ])
  assert.deepEqual(plan.files, [
    { file: api, clientKey: 'p0' },
    { file: guide, clientKey: 'p0' },
    { file: nested, clientKey: 'p1' },
    { file: root },
    { file: ordinary },
  ])
  assert.equal('fileName' in plan.files[0], false)
})

test('maps ensured directories to upload folder_id and keeps direct files at the upload target', () => {
  const nested = relativeFile('api.md', 'Project/docs/api.md')
  const direct = relativeFile('README.md', 'README.md')
  const plan = buildKnowledgeFolderUploadPlan([nested, direct])

  assert.deepEqual(
    mapKnowledgeFolderUploadTargets(
      plan.files,
      [{ client_key: 'p0', folder_id: 'folder-docs' }],
      '',
    ),
    [
      { file: nested, folder_id: 'folder-docs' },
      { file: direct, folder_id: '' },
    ],
  )
})

test('keeps direct files in a concrete current upload target', () => {
  const direct = relativeFile('README.md', 'README.md')
  const plan = buildKnowledgeFolderUploadPlan([direct])

  assert.deepEqual(mapKnowledgeFolderUploadTargets(plan.files, [], 'folder-current'), [
    { file: direct, folder_id: 'folder-current' },
  ])
})

test('rejects a nested upload when ensure-paths omitted its client key', () => {
  const nested = relativeFile('api.md', 'Project/docs/api.md')
  const plan = buildKnowledgeFolderUploadPlan([nested])

  assert.throws(
    () => mapKnowledgeFolderUploadTargets(plan.files, [], ''),
    /missing folder mapping for client key p0/,
  )
})

test('chunks ensure-paths inputs at the 200 path limit while preserving order', () => {
  const paths = Array.from({ length: 201 }, (_, index) => ({
    client_key: `p${index}`,
    segments: [`folder-${index}`],
  }))

  const batches = chunkKnowledgeFolderEnsurePaths(paths)

  assert.deepEqual(batches.map((batch) => batch.length), [200, 1])
  assert.equal(batches[0][0], paths[0])
  assert.equal(batches[1][0], paths[200])
})

test('chunks ensure-paths inputs at the 2000 total segment limit', () => {
  const sharedSegments = Array.from({ length: 20 }, (_, index) => `level-${index}`)
  const paths = Array.from({ length: 101 }, (_, index) => ({
    client_key: `p${index}`,
    segments: [...sharedSegments],
  }))

  assert.deepEqual(
    chunkKnowledgeFolderEnsurePaths(paths).map((batch) => batch.length),
    [100, 1],
  )
})

test('chunks ensure-paths inputs at the 1000 unique prefix-node limit', () => {
  const paths = Array.from({ length: 167 }, (_, pathIndex) => ({
    client_key: `p${pathIndex}`,
    segments: Array.from(
      { length: 6 },
      (_, segmentIndex) => `folder-${pathIndex}-${segmentIndex}`,
    ),
  }))

  assert.deepEqual(
    chunkKnowledgeFolderEnsurePaths(paths).map((batch) => batch.length),
    [166, 1],
  )
})

test('rejects a single ensure path that cannot fit in a backend batch', () => {
  const segments = Array.from({ length: 1001 }, (_, index) => `level-${index}`)

  assert.throws(
    () => chunkKnowledgeFolderEnsurePaths([{ client_key: 'too-deep', segments }]),
    /cannot fit in one ensure-paths batch/,
  )
})
