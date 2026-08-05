import assert from 'node:assert/strict'
import test from 'node:test'

import { hasRequiredResourceSelection, requiresResourceSelection } from './dataSourceResourceSelection.ts'

test('DingTalk requires at least one selected resource', () => {
  assert.equal(requiresResourceSelection('dingtalk'), true)
  assert.equal(hasRequiredResourceSelection('dingtalk', []), false)
  assert.equal(hasRequiredResourceSelection('dingtalk', ['ws-1']), true)
})

test('optional-resource connectors keep accepting empty resource selection', () => {
  assert.equal(requiresResourceSelection('rss'), false)
  assert.equal(hasRequiredResourceSelection('rss', []), true)
})
