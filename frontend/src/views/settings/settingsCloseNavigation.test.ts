import assert from 'node:assert/strict'
import test from 'node:test'

import { resolveSettingsCloseNavigation } from './settingsCloseNavigation'

const KNOWLEDGE_BASES = '/platform/knowledge-bases'
const WHITELIST = [
  'model-catalog',
  'system-global',
  'runtime-queues',
  'platform-api-keys',
  'system-audit-log',
] as const

test('direct-link settings close goes to knowledge bases when there is no in-app back entry', () => {
  for (const historyState of [
    null,
    undefined,
    {},
    { back: null },
    { back: '' },
    { back: 'https://example.com' },
    { back: '//evil.example' },
    { back: 'about:blank' },
  ]) {
    assert.deepEqual(
      resolveSettingsCloseNavigation('/platform/settings', 'members', historyState),
      { type: 'push', to: KNOWLEDGE_BASES },
    )
  }
})

test('in-app settings close uses history back when vue-router recorded a previous route', () => {
  assert.deepEqual(
    resolveSettingsCloseNavigation('/platform/settings', 'members', {
      back: '/platform/knowledge-bases',
      current: '/platform/settings?section=members',
    }),
    { type: 'back' },
  )
  assert.deepEqual(
    resolveSettingsCloseNavigation('/platform/settings', 'general', {
      back: '/platform/settings?section=members',
    }),
    { type: 'back' },
  )
})

test('whitelisted settings sections still go to knowledge bases even when a previous route exists', () => {
  for (const section of WHITELIST) {
    assert.deepEqual(
      resolveSettingsCloseNavigation('/platform/settings', section, {
        back: '/platform/knowledge-bases',
      }),
      { type: 'push', to: KNOWLEDGE_BASES },
    )
    assert.deepEqual(
      resolveSettingsCloseNavigation('/platform/settings', section, { back: null }),
      { type: 'push', to: KNOWLEDGE_BASES },
    )
  }
})

test('closing settings over another route does not navigate', () => {
  assert.deepEqual(
    resolveSettingsCloseNavigation('/platform/knowledge-bases', 'members', { back: null }),
    { type: 'none' },
  )
})
