import assert from 'node:assert/strict'
import test from 'node:test'
import { buildJournalRankBadges } from './journalRankBadges'

const baseRank = {
  schema_version: 1,
  publication: 'Example Journal',
  matched_at: '2026-07-19T00:00:00Z',
  source: 'easyscholar',
  found: true,
}

test('prefers selected official ranks and keeps a stable display order', () => {
  const badges = buildJournalRankBadges({
    ...baseRank,
    official_all: { sci: 'Q3', cssci: '是', sciif: '4.2' },
    official: { sci: 'Q1' },
  })

  assert.deepEqual(badges.map(item => item.label), ['IF 4.2', 'SCI Q1', 'CSSCI'])
})

test('resolves custom data set labels and omits incomplete entries', () => {
  const badges = buildJournalRankBadges({
    ...baseRank,
    custom: [
      { label: 'DUFE', level: 'B' },
      { label: '', level: 'A' },
    ],
  })

  assert.deepEqual(badges, [{ key: 'custom-0-DUFE', label: 'DUFE B', tone: 'custom' }])
})

test('returns no badges for unmatched or missing metadata', () => {
  assert.deepEqual(buildJournalRankBadges(), [])
  assert.deepEqual(buildJournalRankBadges({ ...baseRank, found: false }), [])
})
