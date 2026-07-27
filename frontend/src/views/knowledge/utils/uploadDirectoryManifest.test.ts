import assert from 'node:assert/strict'
import test from 'node:test'
import { buildUploadDirectoryManifest } from './uploadDirectoryManifest.ts'

function file(name: string, relativePath = '', contents = name): File {
  const value = new File([contents], name)
  Object.defineProperty(value, 'webkitRelativePath', { value: relativePath })
  return value
}

test('preserves the selected top-level directory and emits parents before children', () => {
  const files = [
    file('b.txt', 'Project/docs/archive/b.txt'),
    file('root.txt', 'Project/root.txt'),
    file('a.txt', 'Project/docs/a.txt'),
  ]
  const manifest = buildUploadDirectoryManifest(files)
  assert.deepEqual(manifest.directoryPaths, ['Project', 'Project/docs', 'Project/docs/archive'])
  assert.deepEqual(manifest.files.map((entry) => [entry.file.name, entry.relativeDirectoryPath]), [
    ['a.txt', 'Project/docs'],
    ['b.txt', 'Project/docs/archive'],
    ['root.txt', 'Project'],
  ])
})

test('maps ordinary files to the current knowledge folder', () => {
  const plain = file('plain.txt')
  const manifest = buildUploadDirectoryManifest([plain])
  assert.deepEqual(manifest.directoryPaths, [])
  assert.deepEqual(manifest.files, [{ file: plain, relativeFilePath: 'plain.txt', relativeDirectoryPath: '' }])
})

test('deduplicates directories and returns deterministic output without mutating input', () => {
  const first = file('z.txt', 'Top/shared/z.txt')
  const second = file('a.txt', 'Top/shared/a.txt')
  const input = [first, second]
  const snapshot = [...input]
  const one = buildUploadDirectoryManifest(input)
  const two = buildUploadDirectoryManifest([second, first])
  assert.deepEqual(one.directoryPaths, ['Top', 'Top/shared'])
  assert.deepEqual(one.files.map((entry) => entry.relativeFilePath), ['Top/shared/a.txt', 'Top/shared/z.txt'])
  assert.deepEqual(two.files.map((entry) => entry.relativeFilePath), ['Top/shared/a.txt', 'Top/shared/z.txt'])
  assert.deepEqual(input, snapshot)
})

test('rejects unsafe or malformed relative paths', () => {
  const invalid = ['/Top/a.txt', 'C:/Top/a.txt', 'C:Top/a.txt', 'd:Top/a.txt', 'Top\\a.txt', 'Top//a.txt', 'Top/./a.txt', 'Top/../a.txt', `Top/${String.fromCharCode(0)}bad.txt`]
  for (const path of invalid) {
    assert.throws(() => buildUploadDirectoryManifest([file('a.txt', path)]), /path/i, path)
  }
})

test('rejects conflicting duplicate file paths', () => {
  assert.throws(
    () => buildUploadDirectoryManifest([
      file('same.txt', 'Top/same.txt', 'first'),
      file('same.txt', 'Top/same.txt', 'second'),
    ]),
    /duplicate/i,
  )
})


test('rejects file and directory path collisions in either input order', () => {
  const parentFile = file('item', 'Top/item')
  const childFile = file('child.txt', 'Top/item/child.txt')
  assert.throws(() => buildUploadDirectoryManifest([parentFile, childFile]), /conflict/i)
  assert.throws(() => buildUploadDirectoryManifest([childFile, parentFile]), /conflict/i)
})
