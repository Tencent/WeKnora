import assert from 'node:assert/strict'
import test from 'node:test'

import { applyPolledKnowledgeDetails, shouldRefreshWikiStatusAfterKnowledgePoll } from './wikiStatusRefresh.ts'

test('refreshes wiki status when a polled document leaves an in-flight state', () => {
  assert.equal(
    shouldRefreshWikiStatusAfterKnowledgePoll(
      { parse_status: 'finalizing', summary_status: 'processing' },
      { parse_status: 'completed', summary_status: 'completed' },
    ),
    true,
  )
})

test('adopts a replaced file version and refreshes the open document', () => {
  const current = {
    parse_status: 'completed',
    summary_status: 'processing',
    file_version: 1,
    title: 'old.pdf',
  }
  const result = applyPolledKnowledgeDetails(current, {
    parse_status: 'pending',
    file_version: 2,
    file_name: 'new.pdf',
    file_type: 'pdf',
  })
  assert.equal(result.refreshDocument, true)
  assert.deepEqual(current, {
    parse_status: 'pending',
    summary_status: 'processing',
    file_version: 2,
    title: 'new.pdf',
    file_type: 'pdf',
  })
})

test('refreshes chunks when parsing finishes on the same file version', () => {
  const current = { parse_status: 'processing', file_version: 2, title: 'notes.md' }
  const result = applyPolledKnowledgeDetails(current, {
    parse_status: 'completed',
    file_version: 2,
    file_name: 'ignored.md',
  })
  assert.equal(result.refreshDocument, true)
  assert.equal(current.title, 'notes.md')
  assert.equal(current.file_version, 2)
})

test('does not refresh the document for a summary-only poll', () => {
  const current = { parse_status: 'completed', file_version: 2, title: 'notes.md' }
  const result = applyPolledKnowledgeDetails(current, {
    parse_status: 'completed',
    summary_status: 'processing',
    file_version: 2,
  })
  assert.equal(result.refreshDocument, false)
})

test('does not refresh wiki status for ordinary in-flight polling updates', () => {
  assert.equal(
    shouldRefreshWikiStatusAfterKnowledgePoll(
      { parse_status: 'pending' },
      { parse_status: 'processing' },
    ),
    false,
  )
})
