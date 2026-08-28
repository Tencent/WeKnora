import assert from 'node:assert/strict'
import test from 'node:test'

import {
  SETTINGS_SECTION_CAPABILITY,
  isDeploymentCapabilitySupported,
  type DeploymentCapabilityMap,
} from './deploymentCapabilities'

test('capability filtering is fail-open unless backend explicitly disables a feature', () => {
  assert.equal(isDeploymentCapabilitySupported({}, 'organizations'), true)

  const capabilities: DeploymentCapabilityMap = {
    organizations: { supported: false, reason: 'not_supported_in_lite' },
    agents: { supported: true },
  }
  assert.equal(isDeploymentCapabilitySupported(capabilities, 'organizations'), false)
  assert.equal(isDeploymentCapabilitySupported(capabilities, 'agents'), true)
})

test('organizations stay hidden in lite even when capabilities fail open', () => {
  assert.equal(
    isDeploymentCapabilitySupported({}, 'organizations', { liteMode: true }),
    false,
  )
  assert.equal(
    isDeploymentCapabilitySupported({}, 'organizations', { edition: 'lite' }),
    false,
  )
  assert.equal(
    isDeploymentCapabilitySupported({}, 'agents', { liteMode: true }),
    true,
  )
})

test('only route-backed settings sections require deployment capabilities', () => {
  assert.equal(SETTINGS_SECTION_CAPABILITY.mcp, 'settings.mcp')
  assert.equal(SETTINGS_SECTION_CAPABILITY.storage, 'settings.storage')
  assert.equal(SETTINGS_SECTION_CAPABILITY.parser, undefined)
  assert.equal(SETTINGS_SECTION_CAPABILITY['runtime-queues'], undefined)
})

test('skill credentials follow the sandbox capability rather than a key of their own', () => {
  // The values are injected into a skill script's process, so a deployment with
  // no sandbox support has nowhere to put them and the page could only ever show
  // its empty state.
  assert.equal(SETTINGS_SECTION_CAPABILITY.envvars, 'settings.sandbox')
  assert.equal(SETTINGS_SECTION_CAPABILITY.envvars, SETTINGS_SECTION_CAPABILITY.sandbox)
})

test('the skill catalog follows the sandbox capability', () => {
  assert.equal(SETTINGS_SECTION_CAPABILITY.skills, 'settings.sandbox')
  assert.equal(SETTINGS_SECTION_CAPABILITY.skills, SETTINGS_SECTION_CAPABILITY.sandbox)
})
