import assert from 'node:assert/strict'
import test from 'node:test'

import {
  knowledgeNeedsStatusPolling,
  shouldRefreshWikiStatusAfterKnowledgePoll,
} from './wikiStatusRefresh.ts'

test('refreshes wiki status when a polled document leaves an in-flight state', () => {
  assert.equal(
    shouldRefreshWikiStatusAfterKnowledgePoll(
      { parse_status: 'finalizing', summary_status: 'processing' },
      { parse_status: 'completed', summary_status: 'completed' },
    ),
    true,
  )
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

test('keeps polling while a document is being replaced', () => {
  assert.equal(knowledgeNeedsStatusPolling({ parse_status: 'replacing' }), true)
})

test('keeps polling (no wiki refresh) while a replacement moves to pending', () => {
  // replacing -> pending is still in-flight; polling continues, so the wiki
  // status must not be refreshed yet.
  assert.equal(
    shouldRefreshWikiStatusAfterKnowledgePoll(
      { parse_status: 'replacing' },
      { parse_status: 'pending' },
    ),
    false,
  )
})

test('refreshes wiki status when a replacement settles onto completed', () => {
  assert.equal(
    shouldRefreshWikiStatusAfterKnowledgePoll(
      { parse_status: 'replacing' },
      { parse_status: 'completed', summary_status: 'completed' },
    ),
    true,
  )
})
