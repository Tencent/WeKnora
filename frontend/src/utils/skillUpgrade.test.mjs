import assert from 'node:assert/strict'
import test from 'node:test'

import { installOutdated, installUpgradable, upgradeVersions } from './skillUpgrade.ts'

test('an install is outdated only when both digests are known and differ', () => {
  assert.equal(installOutdated({ bundle_sha256: 'b' }, { bundle_sha256: 'a' }), true)
  assert.equal(installOutdated({ bundle_sha256: 'b' }, { bundle_sha256: 'b' }), false)
  assert.equal(installOutdated({ bundle_sha256: 'b' }, {}), false)
  assert.equal(installOutdated({}, { bundle_sha256: 'a' }), false)
})

test('only a ready install can be upgraded', () => {
  const catalog = { bundle_sha256: 'b' }
  assert.equal(installUpgradable(catalog, { status: 'ready', bundle_sha256: 'a' }), true)
  assert.equal(installUpgradable(catalog, { status: 'failed', bundle_sha256: 'a' }), false)
  assert.equal(installUpgradable(catalog, { status: 'installing', bundle_sha256: 'a' }), false)
  assert.equal(installUpgradable(catalog, { status: 'ready', bundle_sha256: 'b' }), false)
})

test('the version pair is shown only when both sides name different versions', () => {
  assert.deepEqual(upgradeVersions({ version: '1.2.0' }, { version: '1.1.0' }), { from: '1.1.0', to: '1.2.0' })
  assert.equal(upgradeVersions({ version: '1.2.0' }, { version: '1.2.0' }), null)
  assert.equal(upgradeVersions({ version: '1.2.0' }, {}), null)
  assert.equal(upgradeVersions({ version: ' ' }, { version: '1.1.0' }), null)
})
