import assert from 'node:assert/strict'
import test from 'node:test'
import { validateDashboardRequest } from './dashboardValidation'

test('dashboard validation allows an inclusive 90-day custom range', () => {
  assert.doesNotThrow(() => validateDashboardRequest({
    range: 'custom',
    from: '2026-05-23',
    to: '2026-08-20',
  }))
})

test('dashboard validation rejects a custom range longer than 90 days', () => {
  assert.throws(
    () => validateDashboardRequest({ range: 'custom', from: '2026-05-22', to: '2026-08-20' }),
    /最长 90 天/,
  )
})
