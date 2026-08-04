import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildUploadFileName,
  flattenFolderRows,
  folderAncestorPaths,
  folderBreadcrumbs,
  folderPathExists,
  isFolderUpload,
} from './folderTree.ts'

const tree = [
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

test('a plain single-file upload at the root sends no path-qualified name', () => {
  assert.equal(buildUploadFileName({ name: 'report.pdf' }, null), undefined)
  assert.equal(buildUploadFileName({ name: 'report.pdf' }, ''), undefined)
})

test('a folder upload keeps the picked directory as the top-level folder', () => {
  assert.equal(
    buildUploadFileName({ name: 'day-one.md', webkitRelativePath: 'handbook/onboarding/day-one.md' }, null),
    'handbook/onboarding/day-one.md',
  )
  assert.equal(
    buildUploadFileName({ name: 'README.md', webkitRelativePath: 'handbook/README.md' }, null),
    'handbook/README.md',
  )
})

test('uploads land in the folder currently open in the sidebar', () => {
  assert.equal(
    buildUploadFileName({ name: 'report.pdf' }, 'handbook/policies'),
    'handbook/policies/report.pdf',
  )
})

test('browsing a folder and uploading a folder nests one inside the other', () => {
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
  // "all documents" and the knowledge base root have no folder crumbs.
  assert.deepEqual(folderBreadcrumbs(null), [])
  assert.deepEqual(folderBreadcrumbs(''), [])
})

test('ancestor paths include the folder itself so the selected row stays visible', () => {
  assert.deepEqual(folderAncestorPaths('a/b/c'), ['a', 'a/b', 'a/b/c'])
  assert.deepEqual(folderAncestorPaths(null), [])
})

test('folder lookup searches the whole depth of the tree', () => {
  assert.equal(folderPathExists(tree, 'handbook/onboarding/day-one'), true)
  assert.equal(folderPathExists(tree, 'design'), true)
  assert.equal(folderPathExists(tree, 'handbook/policies'), false)
})

test('collapsed folders hide their subtree', () => {
  const rows = flattenFolderRows(tree, new Set())
  assert.deepEqual(
    rows.map((row) => [row.node.path, row.depth]),
    [
      ['handbook', 0],
      ['design', 0],
    ],
  )
})

test('expanding a folder reveals its children indented one level deeper', () => {
  const rows = flattenFolderRows(tree, new Set(['handbook', 'handbook/onboarding']))
  assert.deepEqual(
    rows.map((row) => [row.node.path, row.depth]),
    [
      ['handbook', 0],
      ['handbook/onboarding', 1],
      ['handbook/onboarding/day-one', 2],
      ['design', 0],
    ],
  )
})

test('expanding a child without its parent keeps the child hidden', () => {
  const rows = flattenFolderRows(tree, new Set(['handbook/onboarding']))
  assert.deepEqual(
    rows.map((row) => row.node.path),
    ['handbook', 'design'],
  )
})
