import assert from 'node:assert/strict'
import test from 'node:test'

import { connectorDescription, connectorName, isPluginChannel, pickLocale } from './connectorLabels'

const keys: Record<string, string> = { 'datasource.connector.rss': 'RSS', 'datasource.connectorDesc.rss': 'Feeds' }
const i18n = (locale: string) => ({ t: (k: string) => keys[k] ?? k, te: (k: string) => k in keys, locale })

test('builtins use their locale keys', () => {
  assert.equal(connectorName({ type: 'rss', name: 'ignored' }, i18n('zh-CN')), 'RSS')
  assert.equal(connectorDescription({ type: 'rss' }, i18n('en-US')), 'Feeds')
})

test('plugin connectors use their own localized names', () => {
  const plugin = {
    type: 'acme.feeds/feed',
    name: 'ACME Feed',
    names: { 'en-US': 'ACME Feed', 'zh-CN': 'ACME 订阅' },
    description: 'Feeds',
  }
  assert.equal(connectorName(plugin, i18n('zh-CN')), 'ACME 订阅')
  assert.equal(connectorName(plugin, i18n('zh-TW')), 'ACME 订阅')
  assert.equal(connectorName(plugin, i18n('ja-JP')), 'ACME Feed')
  assert.equal(connectorDescription(plugin, i18n('ja-JP')), 'Feeds')
  assert.equal(connectorName({ type: 'acme.x/y' }, i18n('en-US')), 'acme.x/y')
})

test('pickLocale and plugin channels', () => {
  assert.equal(pickLocale({ 'zh-CN': 'a' }, 'zh-HK'), 'a')
  assert.equal(pickLocale(undefined, 'en-US'), undefined)
  assert.equal(isPluginChannel('acme.feeds/feed'), true)
  assert.equal(isPluginChannel('rss'), false)
})
