import assert from 'node:assert/strict'
import test from 'node:test'

import type { ContributionListing, ListedContribution } from '../../api/plugin'
import {
  currentFramePage,
  findPage,
  listedVersion,
  pageFileUrl,
  pageIconUrl,
  pageProvider,
  pagesOf,
  samePage,
  type FramePage,
} from './pluginPages'

const c = (over: Partial<ListedContribution>): ListedContribution => ({
  id: 'links', name: { default: 'Links' }, pluginId: 'acme.links', qualifiedId: 'acme.links/links',
  version: '1.0.0', enabled: true, entry: 'ui/index.html', ...over,
})

const listing: ContributionListing = {
  points: [],
  contributions: {
    pages: [
      c({ id: 'b', qualifiedId: 'acme.links/b', order: 2 }),
      c({ id: 'a', qualifiedId: 'acme.links/a', order: 1, icon: 'ui/icon.svg' }),
      c({ id: 'off', qualifiedId: 'acme.links/off', enabled: false }),
      c({ id: 'noentry', qualifiedId: 'acme.links/noentry', entry: undefined }),
    ],
    settingsSections: [c({ id: 'admin', qualifiedId: 'acme.links/admin', icon: 'icon.svg' })],
  },
}

test('pagesOf keeps enabled pages in order, with role defaults', () => {
  const pages = pagesOf(listing, 'pages')
  assert.deepEqual(pages.map((p) => p.key), ['plugin:acme.links/a', 'plugin:acme.links/b'])
  assert.equal(pages[0].mount, 'pages/a')
  assert.equal(pages[0].minRole, 'viewer')
  assert.equal(pages[0].icon, 'ui/icon.svg')

  const [section] = pagesOf(listing, 'settingsSections')
  assert.equal(section.minRole, 'admin')
  // Only files under ui/ are served, so other icons are dropped.
  assert.equal(section.icon, undefined)
  assert.deepEqual(pagesOf(null, 'kbTabs'), [])
  assert.equal(findPage(pages, 'plugin:acme.links/b')?.id, 'b')
})

test('pageFileUrl escapes each segment', () => {
  const page = pagesOf(listing, 'pages')[0]
  assert.equal(pageFileUrl('/app', page), '/app/api/v1/plugin-ui/assets/acme.links/1.0.0/ui/index.html')
  assert.equal(pageFileUrl('', page, 'ui/a b.js'), '/api/v1/plugin-ui/assets/acme.links/1.0.0/ui/a%20b.js')
})

test('page icons and providers', () => {
  const [withIcon, plain] = pagesOf(listing, 'pages')
  const icon = 'data:image/svg+xml;base64,PHN2Zy8+'
  const named = { ...listing, pluginIcons: { 'acme.links': icon }, pluginNames: { 'acme.links': { default: 'Links', 'zh-CN': '链接' } } }
  assert.equal(pageIconUrl('', named, withIcon), '/api/v1/plugin-ui/assets/acme.links/1.0.0/ui/icon.svg')
  assert.equal(pageIconUrl('', named, plain), icon)
  assert.equal(pageIconUrl('', listing, plain), undefined)
  assert.equal(pageIconUrl('', { ...listing, pluginIcons: { 'acme.links': 'javascript:alert(1)' } }, plain), undefined)
  assert.equal(pageProvider(named, plain, 'zh-CN'), '链接')
  assert.equal(pageProvider(listing, plain, 'zh-CN'), 'acme.links')
})

test('a settings section is registered again when its plugin moves on', () => {
  const [page] = pagesOf(listing, 'settingsSections')
  assert.equal(samePage(page, { ...page }), true)
  // The old page's files are no longer served after an upgrade.
  assert.equal(samePage(page, { ...page, version: '1.1.0' }), false)
})

test('a recorded tool page is served under the current version', () => {
  const tools: ContributionListing = {
    points: [],
    contributions: { mcpServers: [c({ id: 'srv', qualifiedId: 'acme.links/srv', version: '1.2.0', entry: undefined })] },
  }
  const recorded: FramePage = { pluginId: 'acme.links', version: '1.0.0', mount: 'mcpServers/srv', entry: 'ui/tool.html', name: { default: 't' } }
  assert.equal(listedVersion(tools, 'acme.links'), '1.2.0')
  assert.equal(listedVersion(tools, 'acme.gone'), null)
  assert.equal(listedVersion(null, 'acme.links'), undefined)

  assert.deepEqual(currentFramePage(recorded, tools), { ...recorded, version: '1.2.0' })
  // Unknown listing: keep what was recorded; a gone plugin has no page.
  assert.equal(currentFramePage(recorded, null), recorded)
  assert.equal(currentFramePage({ ...recorded, pluginId: 'acme.gone' }, tools), null)
  const same = { ...recorded, version: '1.2.0' }
  assert.equal(currentFramePage(same, tools), same)
})
