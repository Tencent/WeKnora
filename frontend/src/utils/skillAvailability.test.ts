import assert from 'node:assert/strict'
import test from 'node:test'
import { buildCatalogSkillRows } from './skillAvailability.ts'
import type { SkillCatalogItem, SkillInfo } from '../api/skill/index.ts'
const pdf: SkillInfo = { name: 'pdf', source: 'builtin', description: 'Preinstalled PDF', version: '1' }
const catalog = (status?: string, enabled = true): SkillCatalogItem[] => [{
  id: 'cat-pdf', name: 'pdf', description: 'Catalog PDF', created_at: '', updated_at: '',
  installations: status ? [{ skill_id: 'i', sandbox_config_id: 'office', status, enabled, updated_at: '' }] : [],
}]

test('preinstalled skills are selectable with no catalog or installation records', () => {
  const [row] = buildCatalogSkillRows([], [pdf], 'office')
  assert.equal(row.name, 'pdf')
  assert.equal(row.selectable, true)
  assert.equal(row.preinstalled, true)
  assert.equal(row.installed, true)
  assert.equal(row.id, '')
})
test('catalog without an installation does not hide the preinstalled runtime', () => {
  const rows = buildCatalogSkillRows(catalog(), [pdf], 'office')
  assert.equal(rows.length, 1)
  assert.equal(rows[0].selectable, true)
  assert.equal(rows[0].description, pdf.description)
})
test('failed, disabled and removed overrides fall back to the usable preinstalled skill', () => {
  for (const status of ['failed', 'removed', 'installing', 'ready']) {
    const [row] = buildCatalogSkillRows(catalog(status, false), [pdf], 'office')
    assert.equal(row.selectable, true)
    assert.equal(row.preinstalled, true)
    assert.equal(row.installStatus, 'ready')
  }
})
test('installed override uses its own instructions and ready records alone do not prove image availability', () => {
  const override = { name: 'pdf', description: 'Workspace override' }
  const [ready] = buildCatalogSkillRows(catalog('ready'), [override], 'office')
  assert.equal(ready.preinstalled, false)
  assert.equal(ready.description, override.description)
  assert.equal(ready.selectable, true)
  assert.equal(buildCatalogSkillRows(catalog('ready'), [], 'office')[0].selectable, false)
})
test('another sandbox does not inherit preinstalled skills or installation readiness', () => {
  const [row] = buildCatalogSkillRows(catalog('ready'), [], 'empty')
  assert.equal(row.installed, false)
  assert.equal(row.selectable, false)
})
