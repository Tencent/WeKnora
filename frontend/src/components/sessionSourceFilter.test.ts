import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_SESSION_SOURCE,
  buildSourceOptions,
  configuredPlatforms,
  shouldShowSourceFilter,
} from './sessionSourceFilter.ts'

test('defaults the sidebar to web sessions so IM stays hidden until opted into', () => {
  assert.equal(DEFAULT_SESSION_SOURCE, 'web')
})

test('source filter only appears once at least one IM channel is configured', () => {
  assert.equal(shouldShowSourceFilter([]), false)
  assert.equal(shouldShowSourceFilter(['feishu']), true)
  assert.equal(shouldShowSourceFilter(['feishu', 'wecom']), true)
})

test('configuredPlatforms returns distinct platform keys in first-seen order', () => {
  const channels = [
    { platform: 'feishu' },
    { platform: 'wecom' },
    { platform: 'feishu' }, // duplicate: a second feishu channel
    { platform: '' }, // malformed row: ignored
  ]
  assert.deepEqual(configuredPlatforms(channels), ['feishu', 'wecom'])
})

test('configuredPlatforms tolerates an empty overview', () => {
  assert.deepEqual(configuredPlatforms([]), [])
})

test('buildSourceOptions puts web first, then platforms with injected labels', () => {
  const opts = buildSourceOptions(['feishu', 'wecom'], 'My chats', (p) => `LABEL:${p}`)
  assert.deepEqual(opts, [
    { value: 'web', label: 'My chats', platform: null },
    { value: 'feishu', label: 'LABEL:feishu', platform: 'feishu' },
    { value: 'wecom', label: 'LABEL:wecom', platform: 'wecom' },
  ])
})

test('buildSourceOptions with no IM platforms yields just the web option', () => {
  assert.deepEqual(buildSourceOptions([], 'My chats', (p) => p), [
    { value: 'web', label: 'My chats', platform: null },
  ])
})
