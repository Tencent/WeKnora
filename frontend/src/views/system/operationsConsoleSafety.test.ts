import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const component = readFileSync(new URL('./OperationsConsole.vue', import.meta.url), 'utf8')
const api = readFileSync(new URL('../../api/operations/index.ts', import.meta.url), 'utf8')

test('operations console only exposes the protected status and manual backup APIs', () => {
  assert.match(api, /get<OperationsStatus>\('\/api\/v1\/admin\/operations\/status'\)/)
  assert.match(api, /post<ManualBackupResult>\('\/api\/v1\/admin\/operations\/backups', \{ reason \}\)/)
  assert.doesNotMatch(api, /\/api\/v1\/admin\/operations\/(?:restore|rollback|backups\/[^']+\/delete)/i)
})

test('manual backup remains MySQL-only and requires a reason plus confirmation', () => {
  assert.match(component, /status\.value\?\.database\.driver === 'mysql'/)
  assert.match(component, /status\.value\.dependencies\.database === 'ok'/)
  assert.match(component, /!status\.value\.migration\.dirty/)
  assert.match(component, /const validBackupReason/)
  assert.match(component, /<t-popconfirm/)
  assert.match(component, /@confirm="createBackup"/)
})
