import assert from 'node:assert/strict'
import test from 'node:test'

import { shouldRefreshWikiStatusAfterKnowledgePoll } from './wikiStatusRefresh.ts'

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
