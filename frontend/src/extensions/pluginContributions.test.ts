import assert from 'node:assert/strict'
import test from 'node:test'

import type { ContributionListing } from '../api/plugin'
import { findContribution, safeIconData, switchedOffContribution } from './pluginContributions'

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
