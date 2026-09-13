import assert from 'node:assert/strict'
import test from 'node:test'

import { isLegacyPostAuthLanding } from './legacyPostAuthLanding.ts'

const knowledgeBases = { path: '/platform/knowledge-bases' }

test('keeps the knowledge-base default landing after authentication', () => {
  for (const path of ['/login', '/register', '/onboarding/workspace']) {
    assert.equal(isLegacyPostAuthLanding(knowledgeBases, { path }), true)
  }
})

test('does not redirect an intentional video-home menu navigation', () => {
  assert.equal(
    isLegacyPostAuthLanding(knowledgeBases, { path: '/platform/videos' }),
    false,
  )
})

test('does not affect unrelated destinations', () => {
  assert.equal(
    isLegacyPostAuthLanding({ path: '/platform/agents' }, { path: '/login' }),
    false,
  )
})
