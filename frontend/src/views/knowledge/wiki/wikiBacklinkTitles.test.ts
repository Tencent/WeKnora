import assert from 'node:assert/strict'
import test from 'node:test'

import { resolveWikiBacklinkTitle } from './wikiBacklinkTitles.ts'

test('uses the batched title for an unloaded backlink', () => {
  assert.equal(
    resolveWikiBacklinkTitle(
      'concept/gong-si-zi-ben-zhi-du',
      { 'concept/gong-si-zi-ben-zhi-du': '公司资本制度' },
      [],
    ),
    '公司资本制度',
  )
})

test('keeps loaded page titles and slug fallback behavior', () => {
  assert.equal(
    resolveWikiBacklinkTitle('entity/acme', {}, [{ slug: 'entity/acme', title: 'Acme' }]),
    'Acme',
  )
  assert.equal(resolveWikiBacklinkTitle('concept/unknown-page', {}, []), 'unknown-page')
})
