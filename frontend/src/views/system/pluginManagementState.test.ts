import assert from 'node:assert/strict'
import test from 'node:test'

import type { PluginManifest } from '../../api/plugin'
import type { InstalledPlugin, PluginVersion } from '../../api/system/plugins'
import {
  compareVersions,
  contributionLines,
  formatBytes,
  hasSystemConfig,
  installedState,
  isPackageUrl,
  permissionLines,
  remoteHosts,
  remoteUrlReady,
  shortDigest,
  trustNote,
  trustTheme,
  sortVersions,
} from './pluginManagementState'

const manifest: PluginManifest = {
  schemaVersion: 1,
  id: 'acme.search',
  version: '1.2.0',
  name: { default: 'ACME Search' },
  publisher: { id: 'acme' },
  runtime: { type: 'declarative' },
  permissions: { egress: ['*.acme.example'], events: ['knowledge.created'] },
  config: { systemSchema: { type: 'object', properties: { region: { type: 'string' } } } },
  contributes: {
    mcpServers: [{ id: 'search', name: { default: 'Search', 'zh-CN': '搜索' }, mcp: { url: 'https://mcp.acme.example/mcp' } }],
    skills: [{ id: 'triage', name: { default: 'Triage' }, path: 'skills/triage' }],
  },
}

test('formatBytes and shortDigest', () => {
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(1536), '1.5 KB')
  assert.equal(formatBytes(20 * 1024 * 1024), '20 MB')
  assert.equal(shortDigest('sha256:0123456789abcdef'), '0123456789ab')
})

test('contributionLines follow point order and carry the detail', () => {
  assert.deepEqual(contributionLines(manifest, 'zh-CN'), [
    { point: 'skills', id: 'acme.search/triage', name: 'Triage', detail: 'skills/triage' },
    { point: 'mcpServers', id: 'acme.search/search', name: '搜索', detail: 'https://mcp.acme.example/mcp' },
  ])
})

test('permissionLines and remoteHosts flatten what the review shows', () => {
  assert.deepEqual(permissionLines(manifest.permissions), [
    { kind: 'egress', value: '*.acme.example' },
    { kind: 'events', value: 'knowledge.created' },
  ])
  assert.deepEqual(permissionLines(undefined), [])
  assert.deepEqual(remoteHosts(manifest), ['mcp.acme.example'])
})

test('versions sort newest first by semver, not by string', () => {
  const v = (version: string) => ({ version } as PluginVersion)
  assert.deepEqual(sortVersions([v('1.9.0'), v('1.10.0'), v('1.2.3')]).map((x) => x.version), ['1.10.0', '1.9.0', '1.2.3'])
  assert.ok(compareVersions('2.0.0', '1.99.99') > 0)
  assert.equal(compareVersions('1.0.0+build', '1.0.0'), 0)
})

test('installedState reads desired state and the node report', () => {
  const base = { desired_state: 'enabled' } as InstalledPlugin
  assert.equal(installedState({ ...base, desired_state: 'disabled' }), 'disabled')
  assert.equal(installedState(base), 'pending')
  assert.equal(installedState({ ...base, node: { version: '1', state: 'ready', updatedAt: '' } }), 'running')
  assert.equal(installedState({ ...base, node: { version: '1', state: 'failed', updatedAt: '' } }), 'failed')
  assert.equal(installedState({ ...base, node: { version: '1', state: 'degraded', updatedAt: '' } }), 'degraded')
})

test('isPackageUrl and hasSystemConfig', () => {
  assert.equal(isPackageUrl('https://example.com/p.wkp'), true)
  assert.equal(isPackageUrl('ftp://example.com/p.wkp'), false)
  assert.equal(isPackageUrl('not a url'), false)
  assert.equal(hasSystemConfig(manifest), true)
  assert.equal(hasSystemConfig(undefined), false)
})

test('remoteUrlReady asks a new remote plugin for its service URL', () => {
  const remote = (change: string) => ({ manifest: { runtime: { type: 'remote' } }, change })
  assert.equal(remoteUrlReady(remote('install'), ''), false)
  assert.equal(remoteUrlReady(remote('install'), 'plugins.example.com'), false)
  assert.equal(remoteUrlReady(remote('install'), 'https://plugins.example.com'), true)
  assert.equal(remoteUrlReady(remote('upgrade'), ''), true)
  assert.equal(remoteUrlReady(remote('upgrade'), 'nope'), false)
  assert.equal(remoteUrlReady({ manifest: { runtime: { type: 'host' } }, change: 'install' }, ''), true)
  assert.equal(remoteUrlReady(null, ''), true)
})

test('trust tags and signature notes', () => {
  assert.equal(trustTheme('official'), 'success')
  assert.equal(trustTheme('verified'), 'primary')
  assert.equal(trustTheme(undefined), 'default')
  assert.deepEqual(trustNote({ level: 'community' }), { key: 'unsigned', keyId: '' })
  assert.deepEqual(trustNote({ level: 'community', keyId: 'x' }), { key: 'untrustedKey', keyId: 'x' })
  assert.deepEqual(trustNote({ level: 'verified', keyId: 'm', trusted: true }), { key: 'signedBy', keyId: 'm' })
})
