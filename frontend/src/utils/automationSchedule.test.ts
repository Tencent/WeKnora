import assert from 'node:assert/strict'
import test from 'node:test'

import {
  describeTrigger,
  hasEndedOnceSchedule,
  scheduleFormFromTrigger,
  triggerFromScheduleForm,
  wallClockToISO,
  zonedWallClock,
  defaultScheduleForm,
} from './automationSchedule'

test('presets round-trip through cron', () => {
  const cases = [
    { kind: 'hourly', expr: '15 * * * *' },
    { kind: 'daily', expr: '30 9 * * *' },
    { kind: 'weekdays', expr: '0 8 * * 1-5' },
    { kind: 'weekly', expr: '0 18 * * 1,5' },
    { kind: 'monthly', expr: '0 7 1 * *' },
  ]
  for (const { kind, expr } of cases) {
    const form = scheduleFormFromTrigger({ type: 'cron', expr, timezone: 'Asia/Shanghai' })
    assert.equal(form.kind, kind, expr)
    assert.deepEqual(triggerFromScheduleForm(form, 'Asia/Shanghai'), { type: 'cron', expr, timezone: 'Asia/Shanghai' })
  }
})

test('expressions outside the presets stay custom', () => {
  for (const expr of ['*/30 * * * *', '0 9 * * MON', '0 0 9 * * *', '@daily', '0 9 1 1 *']) {
    const form = scheduleFormFromTrigger({ type: 'cron', expr })
    assert.equal(form.kind, 'custom', expr)
    assert.equal(triggerFromScheduleForm(form, 'UTC')?.expr, expr)
  }
})

test('incomplete forms give no trigger', () => {
  assert.equal(triggerFromScheduleForm({ ...defaultScheduleForm(), kind: 'weekly', days: [] }, 'UTC'), null)
  assert.equal(triggerFromScheduleForm({ ...defaultScheduleForm(), kind: 'custom', expr: ' ' }, 'UTC'), null)
  assert.equal(triggerFromScheduleForm({ ...defaultScheduleForm(), kind: 'once', at: '' }, 'UTC'), null)
})

test('once triggers use wall-clock time in their timezone', () => {
  assert.equal(wallClockToISO('2026-03-01 09:00', 'Asia/Shanghai'), '2026-03-01T01:00:00.000Z')
  // New York is UTC-5 in winter and UTC-4 in summer.
  assert.equal(wallClockToISO('2026-01-15 09:00', 'America/New_York'), '2026-01-15T14:00:00.000Z')
  assert.equal(wallClockToISO('2026-07-15 09:00', 'America/New_York'), '2026-07-15T13:00:00.000Z')
  assert.equal(zonedWallClock('2026-07-15T13:00:00Z', 'America/New_York'), '2026-07-15 09:00')
  assert.equal(wallClockToISO('soon', 'UTC'), '')

  const trigger = triggerFromScheduleForm({ ...defaultScheduleForm(), kind: 'once', at: '2026-03-01 09:00' }, 'Asia/Shanghai')
  assert.deepEqual(trigger, { type: 'once', at: '2026-03-01T01:00:00.000Z', timezone: 'Asia/Shanghai' })
  assert.equal(scheduleFormFromTrigger(trigger!).at, '2026-03-01 09:00')
})

test('describeTrigger names the schedule', () => {
  const t = (key: string, params?: Record<string, unknown>) => `${key}${params ? JSON.stringify(params) : ''}`
  assert.equal(
    describeTrigger({ type: 'cron', expr: '5 9 * * 1,3' }, t, 'en-US'),
    'automation.schedule.describe.weekly{"days":"Mon, Wed","time":"09:05"}',
  )
  assert.equal(
    describeTrigger({ type: 'cron', expr: '*/10 * * * *' }, t, 'en-US'),
    'automation.schedule.describe.everyMinutes{"n":10}',
  )
  assert.equal(
    describeTrigger({ type: 'cron', expr: '0 */2 * * *' }, t, 'en-US'),
    'automation.schedule.describe.everyHours{"n":2}',
  )
  assert.equal(
    describeTrigger({ type: 'cron', expr: '*/10 9-17 * * *' }, t, 'en-US'),
    'automation.schedule.describe.custom{"expr":"*/10 9-17 * * *"}',
  )
})

test('only disabled one-shot schedules with no future occurrence have ended', () => {
  const now = Date.parse('2026-10-20T08:00:00Z')
  const once = (at: string, enabled = false) => ({ enabled, trigger: { type: 'once' as const, at } })
  assert.equal(hasEndedOnceSchedule(once('2026-10-20T15:59:59+08:00'), now), true)
  assert.equal(hasEndedOnceSchedule(once('2026-10-20T16:00:00+08:00'), now), true)
  assert.equal(hasEndedOnceSchedule(once('2026-10-20T16:00:01+08:00'), now), false)
  assert.equal(hasEndedOnceSchedule(once('2026-10-20T15:59:59+08:00', true), now), false)
  assert.equal(hasEndedOnceSchedule({ enabled: false, trigger: { type: 'cron', expr: '0 9 * * *' } }, now), false)
})


test('once triggers reject wall-clock times skipped by a timezone transition', () => {
  for (const [local, zone] of [
    ['2026-03-08 02:30', 'America/New_York'],
    ['2026-10-04 02:15', 'Australia/Lord_Howe'],
    ['2011-12-30 12:00', 'Pacific/Apia'],
  ]) {
    assert.equal(wallClockToISO(local, zone), '', `${local} in ${zone}`)
    assert.equal(triggerFromScheduleForm({ ...defaultScheduleForm(), kind: 'once', at: local }, zone), null)
  }
})

test('once triggers reject invalid calendar dates, clock fields and timezone names', () => {
  for (const local of ['2026-02-29 09:00', '2026-04-31 09:00', '2026-13-01 09:00',
    '2026-01-00 09:00', '2026-01-01 24:00', '2026-01-01 09:60', '2026-01-01 09:00oops']) {
    assert.equal(wallClockToISO(local, 'UTC'), '', local)
  }
  assert.equal(wallClockToISO('2026-01-01 09:00', 'Invalid/Timezone'), '')
})

test('valid leap days and times adjacent to transitions retain the requested wall clock', () => {
  for (const [local, zone] of [
    ['2028-02-29 09:00', 'UTC'],
    ['2026-03-08 01:30', 'America/New_York'],
    ['2026-03-08 03:30', 'America/New_York'],
    ['2026-11-01 01:30', 'America/New_York'],
    ['2026-10-04 02:30', 'Australia/Lord_Howe'],
  ]) {
    const iso = wallClockToISO(local, zone)
    assert.ok(iso, `${local} in ${zone}`)
    assert.equal(zonedWallClock(iso, zone), local)
  }
})
