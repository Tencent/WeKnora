import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildFolderRows,
  buildUploadFileName,
  canMoveFolderTo,
  childFolders,
  folderAncestorPaths,
  folderBreadcrumbs,
  folderDocumentCounts,
  folderOptionFromPath,
  folderPathExists,
  isFilteringDocuments,
  isFolderUpload,
  joinFolderPath,
  MAX_FOLDER_DEPTH,
  MAX_FOLDER_PATH_LENGTH,
  MAX_FOLDER_SEGMENT_LENGTH,
  normalizeFolderPath,
  ROOT_FOLDER_PATH,
  sortFolderOptions,
} from './folderTree.ts'

const folders = [
  {
    path: 'handbook',
    name: 'handbook',
    document_count: 1,
    total_count: 4,
    children: [
      {
        path: 'handbook/onboarding',
        name: 'onboarding',
        document_count: 2,
        total_count: 3,
        children: [
          { path: 'handbook/onboarding/day-one', name: 'day-one', document_count: 1, total_count: 1 },
        ],
      },
    ],
  },
  { path: 'design', name: 'design', document_count: 1, total_count: 1 },
]

const tree = { root_document_count: 2, total_document_count: 7, folders }

test('a plain single-file upload into the root sends no path-qualified name', () => {
  assert.equal(buildUploadFileName({ name: 'report.pdf' }, ROOT_FOLDER_PATH), undefined)
})

test('a folder upload keeps the picked directory as the top-level folder', () => {
  assert.equal(
    buildUploadFileName({ name: 'day-one.md', webkitRelativePath: 'handbook/onboarding/day-one.md' }, ''),
    'handbook/onboarding/day-one.md',
  )
  assert.equal(
    buildUploadFileName({ name: 'README.md', webkitRelativePath: 'handbook/README.md' }, ''),
    'handbook/README.md',
  )
})

test('files land in the destination folder confirmed for the batch', () => {
  assert.equal(
    buildUploadFileName({ name: 'report.pdf' }, 'handbook/policies'),
    'handbook/policies/report.pdf',
  )
})

test('a folder upload into a destination folder nests one inside the other', () => {
  assert.equal(
    buildUploadFileName({ name: 'tokens.md', webkitRelativePath: 'design/tokens.md' }, 'handbook'),
    'handbook/design/tokens.md',
  )
})

test('only directory-sourced files count as a folder upload', () => {
  assert.equal(isFolderUpload({ name: 'a.md', webkitRelativePath: 'docs/a.md' }), true)
  assert.equal(isFolderUpload({ name: 'a.md' }), false)
})

test('breadcrumbs walk from the top level down to the selected folder', () => {
  assert.deepEqual(folderBreadcrumbs('handbook/onboarding'), [
    { name: 'handbook', path: 'handbook' },
    { name: 'onboarding', path: 'handbook/onboarding' },
  ])
  // The root row needs no crumbs of its own.
  assert.deepEqual(folderBreadcrumbs(ROOT_FOLDER_PATH), [])
})

test('ancestor paths always include the root so the tree opens down to the selection', () => {
  assert.deepEqual(folderAncestorPaths('a/b/c'), ['', 'a', 'a/b', 'a/b/c'])
  assert.deepEqual(folderAncestorPaths(ROOT_FOLDER_PATH), [''])
})

test('the root always exists, other folders are looked up at any depth', () => {
  assert.equal(folderPathExists(folders, ROOT_FOLDER_PATH), true)
  assert.equal(folderPathExists([], ROOT_FOLDER_PATH), true)
  assert.equal(folderPathExists(folders, 'handbook/onboarding/day-one'), true)
  assert.equal(folderPathExists(folders, 'handbook/policies'), false)
})

test('the root is the only row when it is collapsed', () => {
  const rows = buildFolderRows(tree, new Set())
  assert.equal(rows.length, 1)
  assert.equal(rows[0].kind, 'root')
  assert.equal(rows[0].path, ROOT_FOLDER_PATH)
  assert.equal(rows[0].hasChildren, true)
})

test('top-level folders are children of the root, not its siblings', () => {
  const rows = buildFolderRows(tree, new Set([ROOT_FOLDER_PATH]))
  assert.deepEqual(
    rows.map((row) => [row.path, row.depth]),
    [
      ['', 0],
      ['handbook', 1],
      ['design', 1],
    ],
  )
})

test('expanding nested folders indents each level one step deeper', () => {
  const rows = buildFolderRows(tree, new Set([ROOT_FOLDER_PATH, 'handbook', 'handbook/onboarding']))
  assert.deepEqual(
    rows.map((row) => [row.path, row.depth]),
    [
      ['', 0],
      ['handbook', 1],
      ['handbook/onboarding', 2],
      ['handbook/onboarding/day-one', 3],
      ['design', 1],
    ],
  )
})

test('expanding a folder whose parent is collapsed keeps it hidden', () => {
  const rows = buildFolderRows(tree, new Set([ROOT_FOLDER_PATH, 'handbook/onboarding']))
  assert.deepEqual(rows.map((row) => row.path), ['', 'handbook', 'design'])
})

test('a knowledge base without folders shows a root row with no toggle', () => {
  const rows = buildFolderRows({ root_document_count: 3, total_document_count: 3, folders: [] }, new Set([ROOT_FOLDER_PATH]))
  assert.equal(rows.length, 1)
  assert.equal(rows[0].hasChildren, false)
})

test('an unloaded tree still yields a root row so the sidebar never renders empty', () => {
  const rows = buildFolderRows(null, new Set([ROOT_FOLDER_PATH]))
  assert.deepEqual(rows.map((row) => [row.kind, row.totalCount]), [['root', 0]])
})

test('tree rows carry the recursive total, which is how much a folder contains', () => {
  const [root, handbook] = buildFolderRows(tree, new Set([ROOT_FOLDER_PATH]))
  assert.equal(root.totalCount, 7)
  assert.equal(handbook.totalCount, 4)
  assert.equal(handbook.documentCount, 1)
})

// The breadcrumb and the empty state both report the folder's size, which is the
// recursive total once connector-produced trees nest. `direct` stays available so
// a caller can tell an empty folder from one whose documents live below it.
test('folder counts separate a folder from its whole subtree', () => {
  assert.deepEqual(folderDocumentCounts(tree, ROOT_FOLDER_PATH), { direct: 2, total: 7 })
  assert.deepEqual(folderDocumentCounts(tree, 'handbook'), { direct: 1, total: 4 })
  assert.deepEqual(folderDocumentCounts(tree, 'handbook/onboarding'), { direct: 2, total: 3 })
  assert.deepEqual(folderDocumentCounts(tree, 'handbook/onboarding/day-one'), { direct: 1, total: 1 })

  // A path that is no longer in the tree and an unloaded tree both leave the
  // caller to fall back to the document list it did load.
  assert.equal(folderDocumentCounts(tree, 'handbook/gone'), null)
  assert.equal(folderDocumentCounts(null, ROOT_FOLDER_PATH), null)
  assert.equal(folderDocumentCounts(null, 'handbook'), null)
})

// The regression this guards: the deployed DingTalk tree has a folder whose
// documents all sit one level down, so its direct count is 0. Reading that as
// the folder's size made the sidebar and the empty state claim it was empty.
test('a folder that only groups sub-folders still reports a total', () => {
  const grouped = {
    root_document_count: 0,
    total_document_count: 5,
    folders: [{
      path: '天诛渗透攻防',
      name: '天诛渗透攻防',
      document_count: 0,
      total_count: 5,
      children: [
        { path: '天诛渗透攻防/FAQ', name: 'FAQ', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/售后', name: '售后', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/2024使用培训视频', name: '2024使用培训视频', document_count: 1, total_count: 1 },
      ],
    }],
  }
  assert.deepEqual(folderDocumentCounts(grouped, '天诛渗透攻防'), { direct: 0, total: 5 })
  const counts = folderDocumentCounts(grouped, '天诛渗透攻防')!
  assert.equal(counts.total - counts.direct, 5, 'all five documents are below the folder')
})

// Browsing a folder shows its own sub-folders as list entries, which is what
// lets a document uploaded on its own simply sit at the top level next to the
// folders instead of needing a mode switch to be found.
test('the list shows the direct sub-folders of the browsed folder', () => {
  assert.deepEqual(
    childFolders(tree, ROOT_FOLDER_PATH).map((node) => node.path),
    ['handbook', 'design'],
  )
  assert.deepEqual(
    childFolders(tree, 'handbook').map((node) => node.path),
    ['handbook/onboarding'],
  )
  // A leaf folder and an unknown path both have nothing to descend into.
  assert.deepEqual(childFolders(tree, 'design'), [])
  assert.deepEqual(childFolders(tree, 'nope'), [])
  assert.deepEqual(childFolders(null, ROOT_FOLDER_PATH), [])
})

// The browse/search distinction replaces the scope switch: it is derived from
// what the user is already doing rather than from a control they must interpret.
test('any active filter turns browsing into a subtree search', () => {
  assert.equal(isFilteringDocuments({}), false)
  assert.equal(isFilteringDocuments({ keyword: '   ' }), false, 'whitespace is not a search')
  assert.equal(isFilteringDocuments({ timeRange: [] }), false)
  assert.equal(isFilteringDocuments({ timeRange: ['', ''] }), false)

  assert.equal(isFilteringDocuments({ keyword: 'spec' }), true)
  assert.equal(isFilteringDocuments({ tagIds: ['t1'] }), true)
  assert.equal(isFilteringDocuments({ fileType: 'pdf' }), true)
  assert.equal(isFilteringDocuments({ parseStatus: 'failed' }), true)
  assert.equal(isFilteringDocuments({ source: 'web' }), true)
  assert.equal(isFilteringDocuments({ timeRange: ['2026-01-01', ''] }), true)
})

test('folder paths typed by the user are canonicalized like the server does', () => {
  assert.equal(normalizeFolderPath(' docs / spec '), 'docs/spec')
  assert.equal(normalizeFolderPath('docs//spec/'), 'docs/spec')
  assert.equal(normalizeFolderPath('../../docs'), 'docs')
  assert.equal(normalizeFolderPath('docs\\spec'), 'docs/spec')
  assert.equal(normalizeFolderPath('///'), '')
  assert.equal(joinFolderPath('handbook', 'policies '), 'handbook/policies')
  assert.equal(joinFolderPath('', 'handbook'), 'handbook')
})

test('folder paths typed by the user respect the same depth and length caps as the server', () => {
  const deep = `${'a/'.repeat(MAX_FOLDER_DEPTH + 5)}a`
  const normalizedDeep = normalizeFolderPath(deep)
  assert.equal(normalizedDeep.split('/').filter(Boolean).length, MAX_FOLDER_DEPTH)

  const longSegment = 'x'.repeat(MAX_FOLDER_SEGMENT_LENGTH + 50)
  assert.equal(normalizeFolderPath(longSegment).length, MAX_FOLDER_SEGMENT_LENGTH)

  const wide = 'y'.repeat(100).concat('/').repeat(12).slice(0, -1)
  assert.ok(normalizeFolderPath(wide).length <= MAX_FOLDER_PATH_LENGTH)
})

// Moving a folder into its own subtree would make that subtree unreachable.
test('a folder cannot be moved inside itself', () => {
  assert.equal(canMoveFolderTo('docs', 'handbook'), true)
  assert.equal(canMoveFolderTo('docs', 'docsets'), true, 'a shared prefix is not containment')
  assert.equal(canMoveFolderTo('docs', 'docs/spec'), false)
  assert.equal(canMoveFolderTo('docs', 'docs'), false)
  assert.equal(canMoveFolderTo('docs', ''), false)
  assert.equal(canMoveFolderTo('', 'docs'), false)
})

test('folderOptionFromPath derives picker row metadata from a canonical path', () => {
  assert.deepEqual(folderOptionFromPath('handbook/onboarding'), {
    path: 'handbook/onboarding',
    name: 'onboarding',
    depth: 1,
  })
  assert.deepEqual(
    sortFolderOptions([
      { path: 'handbook/onboarding' },
      { path: 'handbook' },
      { path: 'archive' },
    ]).map((row) => row.path),
    ['archive', 'handbook', 'handbook/onboarding'],
  )
})
