import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildCallComposition,
  normalizeCallComposition,
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

const callLabels = { chat: 'Chat', embedding: 'Embedding', rerank: 'Rerank' }

test('normalizes current call types and a runtime future field without dropping counts', () => {
  const calls = { total: 713, chat: 473, embedding: 149, rerank: 91 }
  const current = normalizeCallComposition(calls, callLabels, 'Other')!
  assert.deepEqual(current.map(item => item.count), [473, 149, 91])
  const future = normalizeCallComposition({ ...calls, total: 723, image: 10 }, callLabels, 'Other')!
  assert.deepEqual(future[3], { type: 'image', label: 'Image', count: 10 })
  assert.equal(buildCallComposition(future, 723)![0].ratio, 473 / 723)
})

test('adds only an unclassified residual and preserves the response denominator', () => {
  const items = normalizeCallComposition({ total: 723, chat: 473, embedding: 149, rerank: 91 }, callLabels, '其他')!
  assert.equal(items[3].label, '其他')
  assert.equal(items[3].count, 10)
  const slices = buildCallComposition(items, 723)!
  assert.equal(slices.reduce((sum, item) => sum + item.count, 0), 723)
  assert.equal(slices[0].ratio, 473 / 723)
  assert.equal(normalizeCallComposition({ total: 10 }, callLabels, 'Other')![0].count, 10)
})

test('rejects overfull breakdowns and invalid totals, and keeps zero empty', () => {
  assert.equal(normalizeCallComposition({ total: 100, chat: 80, embedding: 40 }, callLabels, 'Other'), null)
  for (const total of [null, undefined, NaN, Infinity, -1]) {
    assert.equal(normalizeCallComposition({ total }, callLabels, 'Other'), null)
  }
  assert.deepEqual(normalizeCallComposition({ total: 0, chat: 0 }, callLabels, 'Other'), [])
  assert.equal(normalizeCallComposition({ total: 0, chat: 1 }, callLabels, 'Other'), null)
})

test('handles zero fields, only unknown types, unsafe labels and nonnumeric metadata', () => {
  const items = normalizeCallComposition({
    total: 10, image_generation: 10, speech: 0, chat: null,
    invalid: NaN, negative: -1, metadata: {}, numericString: '3', infinite: Infinity,
  }, callLabels, 'Other')!
  assert.deepEqual(items, [{ type: 'image_generation', label: 'Image generation', count: 10 }])
  assert.equal(buildCallComposition(items, 10)![0].ratio, 1)
  assert.equal(normalizeCallComposition({ total: 1, constructor: 1 }, callLabels, 'Other')![0].label, 'Constructor')
  const collision = normalizeCallComposition({ total: 2, __residual_other: 1 }, callLabels, 'Other')!
  assert.notEqual(collision[0].type, collision[1].type)
})

test('six types and tiny slices retain finite geometry independent of rounded percentages', () => {
  const calls = { total: 1_000_005, chat: 1_000_000, embedding: 1, rerank: 1, image: 1, speech: 1, long_future_call_type: 1 }
  const slices = buildCallComposition(normalizeCallComposition(calls, callLabels, 'Other')!, calls.total)!
  assert.equal(slices.length, 6)
  for (const slice of slices) {
    assert.ok(Number.isFinite(slice.ratio) && slice.ratio > 0 && slice.ratio <= 1)
    assert.ok(Number.isFinite(slice.offset) && slice.offset >= 0 && slice.offset < 1)
  }
  assert.equal(formatRatio(slices[1].ratio), '0%')
  assert.ok(slices[1].ratio > 0)
  assert.equal(slices[5].offset + slices[5].ratio, 1)
  const two = normalizeCallComposition({ total: 3, chat: 1, image: 2 }, callLabels, 'Other')!
  assert.equal(buildCallComposition(two, 3)![1].ratio, 2 / 3)
})
