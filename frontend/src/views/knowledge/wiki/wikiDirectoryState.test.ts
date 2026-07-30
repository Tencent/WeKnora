import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  expandedWikiDirectoryPaths,
  expandWikiDirectoryPath,
} from './wikiDirectoryState.ts'

test('keeps the current wiki directory path expanded without changing sibling state', () => {
  const collapsed = new Set([
    'knowledge:Guides',
    'knowledge:Guides/Setup',
    'knowledge:Reference',
    'summary:Guides',
  ])
  const touched = new Set(['knowledge:Reference'])

  const state = expandWikiDirectoryPath(
    'knowledge',
    ['Guides', 'Setup'],
    collapsed,
    touched,
  )

  assert.deepEqual([...state.collapsed].sort(), [
    'knowledge:Reference',
    'summary:Guides',
  ])
  assert.deepEqual([...state.touched].sort(), [
    'knowledge:Guides',
    'knowledge:Guides/Setup',
    'knowledge:Reference',
  ])
  assert.equal(collapsed.has('knowledge:Guides'), true)
  assert.equal(touched.has('knowledge:Guides'), false)
})

test('returns expanded wiki paths in parent-first order for a refreshed tree', () => {
  const paths = expandedWikiDirectoryPaths(
    'knowledge',
    new Set(['knowledge:Reference']),
    new Set([
      'knowledge:Guides/Setup',
      'summary:Guides',
      'knowledge:Reference',
      'knowledge:Guides',
    ]),
  )

  assert.deepEqual(paths, [
    ['Guides'],
    ['Guides', 'Setup'],
  ])
})

test('folder creation refreshes data while preserving the current directory state', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'WikiBrowser.vue'), 'utf8')
  const createFolder = source.slice(
    source.indexOf('async function createFolder'),
    source.indexOf('// Inline rename:'),
  )

  assert.match(createFolder, /expandWikiDirectoryPath/)
  assert.match(createFolder, /preserveDirectoryState:\s*true/)
  assert.match(createFolder, /scrollTop/)
})
