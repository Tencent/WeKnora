import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildCallComposition,
  defaultAnalyticsDateRange,
  formatCompactNumber,
  formatExactNumber,
  formatLatency,
  formatLocalCalendarDate,
  formatRatio,
  inclusiveDateRangeToExclusiveRFC3339,
} from './modelUsageAnalyticsHelpers'

test('converts an inclusive Sep 1 through Sep 5 selection to an exclusive Sep 6 boundary', () => {
  const bounds = inclusiveDateRangeToExclusiveRFC3339(['2026-09-01', '2026-09-05'])
  assert.equal(bounds.startTime, new Date(2026, 8, 1).toISOString())
  assert.equal(bounds.endTime, new Date(2026, 8, 6).toISOString())
})

test('builds a deterministic 30-calendar-day default range', () => {
  assert.deepEqual(
    defaultAnalyticsDateRange(new Date(2026, 8, 5, 18, 30)),
    ['2026-08-07', '2026-09-05'],
  )
  assert.equal(formatLocalCalendarDate(new Date(2026, 8, 5)), '2026-09-05')
})

test('keeps null distinct from observed zero in metric formatting', () => {
  assert.equal(formatCompactNumber(null), '—')
  assert.equal(formatCompactNumber(0), '0')
  assert.equal(formatLatency(null), '—')
  assert.equal(formatLatency(0), '0 ms')
  assert.equal(formatRatio(null), '—')
  assert.equal(formatRatio(0), '0%')
  assert.equal(formatRatio(0.875), '87.5%')
})

test('call composition retains exact counts and uses the response total as denominator', () => {
  const slices = buildCallComposition([
    { type: 'chat', label: 'Chat', count: 473 },
    { type: 'embedding', label: 'Embedding', count: 148 },
    { type: 'rerank', label: 'Rerank', count: 92 },
  ], 713)!
  assert.equal(slices.reduce((sum, slice) => sum + slice.count, 0), 713)
  assert.deepEqual(slices.map(slice => formatRatio(slice.ratio)), ['66.3%', '20.8%', '12.9%'])
  assert.equal(slices[2].offset, 621 / 713)
})

test('call composition omits zeros and supports any number of supplied types', () => {
  const items = Array.from({ length: 6 }, (_, index) => ({
    type: `type-${index}`, label: `Type ${index}`, count: index,
  }))
  const slices = buildCallComposition(items, 15)!
  assert.equal(slices.length, 5)
  assert.equal(slices[0].offset, 0)
  assert.equal(slices[4].offset + slices[4].ratio, 1)
  assert.deepEqual(buildCallComposition([{ type: 'chat', label: 'Chat', count: 0 }], 0), [])
  assert.equal(buildCallComposition([{ type: 'chat', label: 'Chat', count: 42 }], 42)![0].ratio, 1)
})

test('call composition rejects incomplete or invalid counts without inventing a remainder', () => {
  for (const count of [2, -1, NaN, Infinity, 0.5, null]) {
    assert.equal(buildCallComposition([
      { type: 'chat', label: 'Chat', count: count as number },
    ], 3), null)
  }
  assert.equal(buildCallComposition([], NaN), null)
})

test('formats large token values compactly while retaining an exact formatter', () => {
  assert.equal(formatCompactNumber(1_200), '1.2K')
  assert.equal(formatCompactNumber(236_213_313), '236.2M')
  assert.equal(formatExactNumber(236_213_313), '236,213,313')
  assert.equal(formatLatency(1320), '1.32 s')
})
