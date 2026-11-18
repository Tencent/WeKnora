import assert from 'node:assert/strict'
import test from 'node:test'
import { humanizeCron, relativeTime } from './cronHumanize'

// Minimal i18n translator that supports {n} interpolation, mirroring the
// signature used by DataSourceSettings.vue for list rendering.
function makeT() {
  return (key: string, params?: Record<string, string | number>): string => {
    if (key === 'datasource.scheduleHuman.30min') return 'Every 30 min'
    if (key === 'datasource.scheduleHuman.1h' || key === 'datasource.scheduleHuman.6h') return 'Hourly'
    if (key === 'datasource.scheduleHuman.12h') return 'Every 12 hours'
    if (key === 'datasource.scheduleHuman.24h') return `Daily at ${params?.time}`
    if (key === 'datasource.scheduleHours' && params) return `Every ${params.n} hours`
    if (key === 'datasource.neverSynced') return 'Never synced'
    if (key === 'datasource.justNow') return 'Just now'
    if (key === 'datasource.minutesAgo') return `${params?.n} min ago`
    if (key === 'datasource.hoursAgo') return `${params?.n} h ago`
    if (key === 'datasource.daysAgo') return `${params?.n} d ago`
    return key
  }
}

test('humanizeCron maps known preset cron expressions to friendly labels', () => {
  const t = makeT()
  assert.equal(humanizeCron('0 */30 * * * *', t), 'Every 30 min')
  assert.equal(humanizeCron('0 0 * * * *', t), 'Hourly')
  assert.equal(humanizeCron('0 0 */6 * * *', t), 'Hourly')
  assert.equal(humanizeCron('0 0 */12 * * *', t), 'Every 12 hours')
  assert.equal(humanizeCron('0 0 2 * * *', t), 'Daily at 02:00')
  assert.equal(humanizeCron('0 45 9 * * *', t), 'Daily at 09:45')
})

test('humanizeCron renders a custom hourly interval as "Every N hours"', () => {
  const t = makeT()
  assert.equal(humanizeCron('0 0 */3 * * *', t), 'Every 3 hours')
  assert.equal(humanizeCron('0 0 */13 * * *', t), 'Every 13 hours')
  assert.equal(humanizeCron('0 0 */24 * * *', t), 'Every 24 hours')
})

test('humanizeCron leaves unknown expressions and empty input untouched', () => {
  const t = makeT()
  assert.equal(humanizeCron('0 0 1 * * MON', t), '0 0 1 * * MON')
  assert.equal(humanizeCron('CRON_TZ=UTC 0 0 1 * * *', t), 'CRON_TZ=UTC 0 0 1 * * *')
  assert.equal(humanizeCron('', t), '--')
  assert.equal(humanizeCron(null as unknown as string, t), '--')
})
