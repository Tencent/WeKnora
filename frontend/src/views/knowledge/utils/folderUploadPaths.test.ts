import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildUploadDirectoryTreeRows,
  countUploadDirectories,
  getUploadDirectorySegments,
  getUploadFileDisplayName,
  getLegacyFolderUploadFileName,
  getUtf8ByteLength,
  makeUploadFolderCopyName,
} from './folderUploadPaths.ts'

function mockFile(name: string, relativePath = '', size = 1): File {
  return {
    name,
    size,
    webkitRelativePath: relativePath,
  } as File
}

test('folder upload keeps the selected top-level folder in the destination path', () => {
  const file = mockFile('plan.pdf', '市场资料/2026/plan.pdf')

  assert.deepEqual(getUploadDirectorySegments(file), ['市场资料', '2026'])
  assert.equal(getUploadFileDisplayName(file), '市场资料/2026/plan.pdf')
})

test('ordinary file upload has no folder destination segments', () => {
  const file = mockFile('plan.pdf')

  assert.deepEqual(getUploadDirectorySegments(file), [])
  assert.equal(getUploadFileDisplayName(file), 'plan.pdf')
})

test('legacy folder upload strips the selected root folder', () => {
  assert.equal(
    getLegacyFolderUploadFileName(mockFile('plan.pdf', '市场资料/2026/plan.pdf')),
    '2026/plan.pdf',
  )
  assert.equal(
    getLegacyFolderUploadFileName(mockFile('overview.pdf', '市场资料/overview.pdf')),
    undefined,
  )
})

test('folder upload counts each unique directory prefix once', () => {
  const files = [
    mockFile('overview.pdf', '市场资料/overview.pdf'),
    mockFile('plan.pdf', '市场资料/2026/plan.pdf'),
    mockFile('notes.txt', '市场资料/2026/notes.txt'),
    mockFile('readme.md', '研发资料/readme.md'),
  ]

  assert.equal(countUploadDirectories(files), 3)
})

test('folder upload preview builds folder and file rows with their hierarchy', () => {
  const files = [
    mockFile('overview.pdf', '市场资料/overview.pdf', 10),
    mockFile('plan.pdf', '市场资料/2026/plan.pdf', 20),
    mockFile('notes.txt', '市场资料/2026/notes.txt', 30),
    mockFile('readme.md', '研发资料/readme.md', 40),
  ]

  assert.deepEqual(
    buildUploadDirectoryTreeRows(files).map(row => ({
      kind: row.kind,
      name: row.name,
      path: row.path,
      depth: row.depth,
      fileIndex: row.kind === 'file' ? row.fileIndex : undefined,
    })),
    [
      { kind: 'folder', name: '市场资料', path: '市场资料', depth: 0, fileIndex: undefined },
      { kind: 'folder', name: '2026', path: '市场资料/2026', depth: 1, fileIndex: undefined },
      { kind: 'file', name: 'plan.pdf', path: '市场资料/2026/plan.pdf', depth: 2, fileIndex: 1 },
      { kind: 'file', name: 'notes.txt', path: '市场资料/2026/notes.txt', depth: 2, fileIndex: 2 },
      { kind: 'file', name: 'overview.pdf', path: '市场资料/overview.pdf', depth: 1, fileIndex: 0 },
      { kind: 'folder', name: '研发资料', path: '研发资料', depth: 0, fileIndex: undefined },
      { kind: 'file', name: 'readme.md', path: '研发资料/readme.md', depth: 1, fileIndex: 3 },
    ],
  )
})

test('folder upload preview excludes ordinary files and keeps source indexes for removal', () => {
  const files = [
    mockFile('standalone.pdf'),
    mockFile('nested.pdf', '资料/子目录/nested.pdf'),
  ]

  const rows = buildUploadDirectoryTreeRows(files)
  const fileRow = rows.find(row => row.kind === 'file')

  assert.equal(rows.some(row => row.name === 'standalone.pdf'), false)
  assert.equal(fileRow?.kind === 'file' ? fileRow.fileIndex : undefined, 1)
  assert.equal(fileRow?.kind === 'file' ? fileRow.file : undefined, files[1])
})

test('folder conflict rename keeps the generated name within the backend limit', () => {
  assert.equal(makeUploadFolderCopyName('市场资料', 1), '市场资料 (1)')

  const renamed = makeUploadFolderCopyName('a'.repeat(255), 12)
  assert.equal(getUtf8ByteLength(renamed), 255)
  assert.ok(renamed.endsWith(' (12)'))

  const renamedChinese = makeUploadFolderCopyName('资'.repeat(85), 12)
  assert.ok(getUtf8ByteLength(renamedChinese) <= 255)
  assert.ok(renamedChinese.endsWith(' (12)'))
})

test('UTF-8 byte length matches the backend folder-name limit', () => {
  assert.equal(getUtf8ByteLength('a'.repeat(255)), 255)
  assert.equal(getUtf8ByteLength('资'.repeat(85)), 255)
  assert.equal(getUtf8ByteLength('资'.repeat(86)), 258)
})
