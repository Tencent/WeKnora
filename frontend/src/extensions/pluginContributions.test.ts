import assert from 'node:assert/strict'
import test from 'node:test'

import type { ContributionListing } from '../api/plugin'
import {
  chunkerStrategies,
  findContribution,
  safeIconData,
  switchedOffContribution,
  switchedOffIcon,
} from './pluginContributions'

const listing = {
  points: [],
  contributions: {
    connectors: [
      { id: 'jira', qualifiedId: 'acme.jira/jira', pluginId: 'acme.jira', enabled: false, name: { default: 'Jira' } },
      { id: 'notion', qualifiedId: 'weknora.notion/notion', aliases: ['notion'], pluginId: 'weknora.notion', enabled: true, name: { default: 'Notion' } },
      { id: 'feishu', qualifiedId: 'weknora.feishu/feishu', aliases: ['feishu'], pluginId: 'weknora.feishu', enabled: false, name: { default: 'Feishu' } },
    ],
  },
} as unknown as ContributionListing

test('instance types resolve by qualified ID or builtin alias', () => {
  assert.equal(findContribution(listing, 'connectors', 'acme.jira/jira')?.pluginId, 'acme.jira')
  assert.equal(findContribution(listing, 'connectors', 'notion')?.pluginId, 'weknora.notion')
  assert.equal(findContribution(listing, 'webSearch', 'notion'), undefined)
  assert.equal(findContribution(null, 'connectors', 'notion'), undefined)
})

test('only contributions of switched-off plugins are reported', () => {
  assert.equal(switchedOffContribution(listing, 'connectors', 'acme.jira/jira')?.id, 'jira')
  assert.equal(switchedOffContribution(listing, 'connectors', 'feishu')?.id, 'feishu')
  assert.equal(switchedOffContribution(listing, 'connectors', 'notion'), undefined)
  assert.equal(switchedOffContribution(listing, 'connectors', 'unknown'), undefined)
})

test('plugin icons are image data URIs only', () => {
  assert.equal(safeIconData('data:image/svg+xml;base64,PHN2Zy8+'), 'data:image/svg+xml;base64,PHN2Zy8+')
  for (const bad of ['data:text/html;base64,PGgxPg==', 'javascript:alert(1)', 'https://x/icon.svg', '', undefined]) {
    assert.equal(safeIconData(bad), undefined)
  }
})

test('switched-off plugins lend their icon to their instances', () => {
  const withIcons = { ...listing, pluginIcons: { 'acme.jira': 'data:image/png;base64,iVBOR', 'weknora.notion': 'data:image/png;base64,AAAA' } }
  assert.equal(switchedOffIcon(withIcons, 'connectors', 'acme.jira/jira'), 'data:image/png;base64,iVBOR')
  assert.equal(switchedOffIcon(withIcons, 'connectors', 'notion'), undefined, 'the plugin is on')
  assert.equal(switchedOffIcon(listing, 'connectors', 'acme.jira/jira'), undefined)
})

test('chunkerStrategies lists switched-on chunkers and the current one', () => {
  const listing = {
    points: [],
    contributions: {
      chunkers: [
        { id: 'clauses', qualifiedId: 'acme.legal/clauses', pluginId: 'acme.legal', enabled: true, name: { default: 'By clause', 'zh-CN': '按条款' } },
        { id: 'pages', qualifiedId: 'acme.pdf/pages', pluginId: 'acme.pdf', enabled: false, name: { default: 'By page' } },
      ],
    },
  } as unknown as ContributionListing
  assert.deepEqual(chunkerStrategies(listing, 'zh-CN').map((c) => [c.value, c.label]), [
    ['plugin:acme.legal/clauses', '按条款'],
  ])
  const withCurrent = chunkerStrategies(listing, 'en-US', 'plugin:acme.pdf/pages')
  assert.deepEqual(withCurrent.map((c) => [c.value, c.enabled]), [
    ['plugin:acme.legal/clauses', true],
    ['plugin:acme.pdf/pages', false],
  ])
  assert.deepEqual(chunkerStrategies(null, 'en-US'), [])
})
