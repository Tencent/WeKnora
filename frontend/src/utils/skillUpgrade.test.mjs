import assert from 'node:assert/strict'
import test from 'node:test'

import {
  installOutdated, installUpgradable, servedPrevious, servedPreviousText, upgradeVersions,
} from './skillUpgrade.ts'

test('an install is outdated only when both digests are known and differ', () => {
  assert.equal(installOutdated({ bundle_sha256: 'b' }, { bundle_sha256: 'a' }), true)
  assert.equal(installOutdated({ bundle_sha256: 'b' }, { bundle_sha256: 'b' }), false)
  assert.equal(installOutdated({ bundle_sha256: 'b' }, {}), false)
  assert.equal(installOutdated({}, { bundle_sha256: 'a' }), false)
})

test('a ready or failed install that differs from the catalog can be upgraded', () => {
  const catalog = { bundle_sha256: 'b' }
  assert.equal(installUpgradable(catalog, { status: 'ready', bundle_sha256: 'a' }), true)
  assert.equal(installUpgradable(catalog, { status: 'failed', bundle_sha256: 'a' }), true)
  assert.equal(installUpgradable(catalog, { status: 'failed', bundle_sha256: 'b' }), false)
  assert.equal(installUpgradable(catalog, { status: 'removing', bundle_sha256: 'a' }), false)
  assert.equal(installUpgradable(catalog, { status: 'installing', bundle_sha256: 'a' }), false)
  assert.equal(installUpgradable(catalog, { status: 'ready', bundle_sha256: 'b' }), false)
})

test('the version pair is shown only when both sides name different versions', () => {
  assert.deepEqual(upgradeVersions({ version: '1.2.0' }, { version: '1.1.0' }), { from: '1.1.0', to: '1.2.0' })
  assert.equal(upgradeVersions({ version: '1.2.0' }, { version: '1.2.0' }), null)
  assert.equal(upgradeVersions({ version: '1.2.0' }, {}), null)
  assert.equal(upgradeVersions({ version: ' ' }, { version: '1.1.0' }), null)
})

test('the previous version is reported only while an install has not replaced it', () => {
  assert.deepEqual(
    servedPrevious({ status: 'installing', served: { version: '1.0.0' } }),
    { upgrading: true, version: '1.0.0' },
  )
  assert.deepEqual(servedPrevious({ status: 'failed', served: {} }), { upgrading: false, version: '' })
  assert.equal(servedPrevious({ status: 'ready', served: { version: '1.0.0' } }), null)
  assert.equal(servedPrevious({ status: 'removing', served: { version: '1.0.0' } }), null)
  assert.equal(servedPrevious({ status: 'installing' }), null)
})

test('the served note names the version when there is one', () => {
  const t = (key, params) => (params ? `${key}:${params.version}` : key)
  assert.equal(
    servedPreviousText(t, { status: 'installing', served: { version: '1.0.0' } }),
    'settings.skills.servedWhileUpgrading:1.0.0',
  )
  assert.equal(servedPreviousText(t, { status: 'failed', served: {} }), 'settings.skills.servedAfterFailurePlain')
  assert.equal(servedPreviousText(t, { status: 'ready' }), '')
})
