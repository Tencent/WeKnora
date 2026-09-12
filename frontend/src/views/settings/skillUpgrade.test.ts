import assert from 'node:assert/strict'
import test from 'node:test'
import { isSkillInstallOutdated } from './skillUpgrade.ts'

test('different archive with the same version is upgradeable', () => {
  assert.equal(isSkillInstallOutdated({ version: '1', bundle_sha256: 'new' }, { version: '1', bundle_sha256: 'old' }), true)
  assert.equal(isSkillInstallOutdated({ version: '2', bundle_sha256: 'same' }, { version: '1', bundle_sha256: 'same' }), false)
})
test('older responses fall back to known version labels without inventing a version', () => {
  assert.equal(isSkillInstallOutdated({ version: '2026.09.7' }, { version: '2026.09.6' }), true)
  assert.equal(isSkillInstallOutdated({ version: '2026.09.7' }, {}), false)
})
