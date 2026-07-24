import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const cardSource = readFileSync(join(here, 'McpOAuthCard.vue'), 'utf8')
const settingsSource = readFileSync(join(here, '../../settings/components/McpServiceDialog.vue'), 'utf8')
const apiSource = readFileSync(join(here, '../../../api/mcp-service.ts'), 'utf8')

test('in-chat OAuth polling is bound to the newly opened authorization attempt', () => {
  assert.match(cardSource, /authorization\.authorizationAttempt/)
  assert.match(
    cardSource,
    /getMCPOAuthStatus\(props\.serviceId, authorization\.authorizationAttempt\)/,
  )
  assert.match(
    cardSource,
    /props\.serviceId,\s*authorization\.authorizationAttempt,\s*\)/,
  )
})

test('settings OAuth polling cannot accept a pre-existing token as fresh authorization', () => {
  assert.match(
    settingsSource,
    /getMCPOAuthStatus\(serviceId, authorization\.authorizationAttempt\)/,
  )
})

test('OAuth status API sends the attempt id to the backend', () => {
  assert.match(apiSource, /authorization_attempt=\$\{encodeURIComponent\(authorizationAttempt\)\}/)
})

test('settings distinguishes refreshable tokens from usable authorization', () => {
  assert.match(settingsSource, /getMCPOAuthAuthorizationStatus\(props\.service\.id\)/)
  assert.match(settingsSource, /oauthTokenState === 'refreshable'/)
  assert.match(apiSource, /state: data\?\.state \?\? 'reauth_required'/)
})
