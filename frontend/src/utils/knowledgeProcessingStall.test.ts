import assert from 'node:assert/strict'
import test from 'node:test'
import { shownStall, stalledMinutes, PROCESSING_STALL_THRESHOLD_MS } from './knowledgeProcessingStall.ts'

const now = Date.parse('2026-09-22T10:00:00Z')
const ago = (ms: number) => new Date(now - ms).toISOString()

test('in-flight document idle past the threshold reports whole minutes', () => {
  assert.equal(stalledMinutes({ parse_status: 'processing', last_activity_at: ago(47 * 60000 + 30000) }, now), 47)
  assert.equal(stalledMinutes({ parse_status: 'finalizing', last_activity_at: ago(PROCESSING_STALL_THRESHOLD_MS) }, now), 20)
  assert.equal(stalledMinutes({ parse_status: 'pending', last_activity_at: ago(2 * 3600000) }, now), 120)
})

test('recent activity, finished rows and unknown activity are not stalled', () => {
  assert.equal(stalledMinutes({ parse_status: 'processing', last_activity_at: ago(PROCESSING_STALL_THRESHOLD_MS - 1) }, now), 0)
  assert.equal(stalledMinutes({ parse_status: 'completed', last_activity_at: ago(3600000) }, now), 0)
  assert.equal(stalledMinutes({ parse_status: 'failed', last_activity_at: ago(3600000) }, now), 0)
  assert.equal(stalledMinutes({ parse_status: 'processing' }, now), 0)
  assert.equal(stalledMinutes({ parse_status: 'processing', last_activity_at: 'not a date' }, now), 0)
})

test('only a server verdict on a still-quiet document is shown', () => {
  assert.equal(shownStall('queued', 25), 'queued')
  assert.equal(shownStall('stalled', 25), 'stalled')
  assert.equal(shownStall(undefined, 25), '', 'no verdict: the probe failed or was not run')
  assert.equal(shownStall('stalled', 0), '', 'progress resumed since the verdict')
  assert.equal(shownStall('bogus', 25), '')
})
