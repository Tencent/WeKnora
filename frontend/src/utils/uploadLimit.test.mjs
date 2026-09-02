import assert from 'node:assert/strict'
import test from 'node:test'

import { isSkillBundleUploadUrl } from './uploadLimit.ts'

test('skill catalog and sandbox skill uploads use the skill-bundle 413 path', () => {
  assert.equal(isSkillBundleUploadUrl('/api/v1/skills/catalog'), true)
  assert.equal(isSkillBundleUploadUrl('/api/v1/skills/catalog?foo=1'), true)
  assert.equal(isSkillBundleUploadUrl('/api/v1/sandbox-configs/cfg-a/skills'), true)
  assert.equal(isSkillBundleUploadUrl('https://host/api/v1/sandbox-configs/cfg-a/skills'), true)
})

test('knowledge and other API uploads stay on the knowledge-size 413 path', () => {
  assert.equal(isSkillBundleUploadUrl(undefined), false)
  assert.equal(isSkillBundleUploadUrl('/api/v1/knowledge'), false)
  assert.equal(isSkillBundleUploadUrl('/api/v1/knowledge-bases/kb-1/knowledge'), false)
  assert.equal(isSkillBundleUploadUrl('/api/v1/models/debug'), false)
})
