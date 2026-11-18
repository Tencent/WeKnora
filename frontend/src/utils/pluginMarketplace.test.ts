import assert from 'node:assert/strict'
import test from 'node:test'
import JSZip from 'jszip'
import { readFileSync } from 'node:fs'
import {
  filledEnv,
  folderHasMarketplaceManifest,
  installedPluginCount,
  isMarketplaceArchiveName,
  isMarketplaceSource,
  MARKETPLACE_ARCHIVE_ACCEPT,
  marketplaceHasInstalls,
  missingRequiredEnv,
  pluginCardState,
  pluginHasNewerVersion,
  pluginStateIsTransient,
  zipMarketplaceFolder,
} from './pluginMarketplace'

test('marketplace sources accept what the backend resolves and nothing else', () => {
  for (const ok of ['acme/plugins', 'https://github.com/acme/plugins', 'http://gitlab.local/a/b', 'https://x.dev/m.zip']) {
    assert.equal(isMarketplaceSource(ok), true, ok)
  }
  for (const bad of ['', '   ', 'acme', 'file:///etc/passwd', 'git@github.com:acme/plugins.git', 'a/b/c']) {
    assert.equal(isMarketplaceSource(bad), false, bad)
  }
})

test('archive names are limited to the formats the backend unpacks', () => {
  for (const ok of ['m.zip', 'M.TAR.GZ', 'm.tgz', 'm.tar']) assert.equal(isMarketplaceArchiveName(ok), true, ok)
  for (const bad of ['m.rar', 'm.gz', 'zip']) assert.equal(isMarketplaceArchiveName(bad), false, bad)
})

test('the file dialog accept list can select .tar.gz without treating plain .gz as valid', () => {
  for (const token of ['.gz', 'application/gzip', 'application/x-compressed-tar', '.tar.gz']) {
    assert.ok(MARKETPLACE_ARCHIVE_ACCEPT.split(',').includes(token), token)
  }
  assert.equal(isMarketplaceArchiveName('plugins.tar.gz'), true)
  assert.equal(isMarketplaceArchiveName('plugins.gz'), false)
  const vue = readFileSync(new URL('../views/settings/components/PluginMarketplaceDialog.vue', import.meta.url), 'utf8')
  assert.match(vue, /:accept="MARKETPLACE_ARCHIVE_ACCEPT"/)
})

test('a picked folder must carry a marketplace or a plugin at its root or one level down', () => {
  assert.equal(folderHasMarketplaceManifest(['acme/.weknora-plugin/marketplace.json', 'acme/plugins/x']), true)
  assert.equal(folderHasMarketplaceManifest(['.weknora-plugin/marketplace.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/.claude-plugin/marketplace.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/.cursor-plugin/marketplace.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/.github/plugin/marketplace.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/.agents/plugins/marketplace.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/plugin.json', 'acme/skills/search/SKILL.md']), true)
  assert.equal(folderHasMarketplaceManifest(['alpha/plugin.json', 'beta/skills/b/SKILL.md']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/plugins/neon/.weknora-plugin/marketplace.json']), false)
  assert.equal(folderHasMarketplaceManifest(['acme/.weknora-plugin/plugin.json']), true)
  assert.equal(folderHasMarketplaceManifest(['acme/README.md']), false)
})

test('folder zips keep relative paths and drop traversal entries', async () => {
  const blob = await zipMarketplaceFolder([
    { path: 'acme/.weknora-plugin/marketplace.json', data: '{"name":"acme"}' },
    { path: 'acme\\plugins\\neon\\SKILL.md', data: '# neon' },
    { path: 'acme/../evil.txt', data: 'x' },
  ])
  const zip = await JSZip.loadAsync(await blob.arrayBuffer())
  const names = Object.keys(zip.files).filter((name) => !zip.files[name]!.dir).sort()
  assert.deepEqual(names, ['acme/.weknora-plugin/marketplace.json', 'acme/plugins/neon/SKILL.md'])
  assert.equal(await zip.file('acme/.weknora-plugin/marketplace.json')!.async('string'), '{"name":"acme"}')
})

test('required env gating and blank optional values', () => {
  const decls = [
    { name: 'NEON_API_KEY', required: true },
    { name: 'NEON_REGION' },
  ]
  assert.deepEqual(missingRequiredEnv(decls, { NEON_API_KEY: '  ', NEON_REGION: '' }), ['NEON_API_KEY'])
  assert.deepEqual(missingRequiredEnv(decls, { NEON_API_KEY: 'k' }), [])
  assert.deepEqual(missingRequiredEnv(undefined, {}), [])
  assert.deepEqual(filledEnv({ NEON_API_KEY: 'k', NEON_REGION: ' ' }), { NEON_API_KEY: 'k' })
})

test('the toolbox badge counts installed plugins on the marketplace list', () => {
  assert.equal(installedPluginCount([
    { plugins: [{ installed: true }, { installed: false }] },
    { plugins: [{ installed: true }] },
    {},
  ]), 2)
  assert.equal(installedPluginCount([]), 0)
})

test('card state follows the install row', () => {
  const base = { name: 'p', skill_count: 0, mcp_count: 1, requires_sandbox: false }
  assert.equal(pluginCardState({ ...base, installed: false }), 'available')
  assert.equal(pluginCardState({ ...base, installed: true }), 'ready')
  assert.equal(pluginCardState({ ...base, installed: true, status: 'failed' }), 'failed')
  assert.equal(pluginCardState({ ...base, installed: true, status: 'needs_oauth' }), 'needs_oauth')
  assert.equal(pluginStateIsTransient('installing'), true)
  assert.equal(pluginStateIsTransient('ready'), false)
  assert.equal(pluginStateIsTransient('needs_oauth'), false)
})

test('deleting a marketplace is blocked by installs hidden by the search filter', () => {
  const all = [{ id: 'mp', plugins: [{ installed: false }, { installed: true }] }, { id: 'empty', plugins: [] }]
  assert.equal(marketplaceHasInstalls(all, 'mp'), true)
  assert.equal(marketplaceHasInstalls(all, 'empty'), false)
  assert.equal(marketplaceHasInstalls(all, 'missing'), false)
})

test('a newer marketplace version is flagged only on installed plugins', () => {
  assert.equal(pluginHasNewerVersion({ installed: true, version: '2.0.0', installed_version: '1.0.0' }), true)
  assert.equal(pluginHasNewerVersion({ installed: true, version: '1.0.0', installed_version: '1.0.0' }), false)
  assert.equal(pluginHasNewerVersion({ installed: false, version: '2.0.0' }), false)
  assert.equal(pluginHasNewerVersion({ installed: true, version: '2.0.0' }), false)
})
