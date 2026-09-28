import assert from 'node:assert/strict'
import test from 'node:test'
import {
  HOST_SKILL_TARGET_ID,
  hostSkillTargetRecord,
  hostSkillsOnly,
  isHostSkillTarget,
  mentionSkillTargetId,
} from './skillTarget'

test('host skill target record carries the reserved id and type', () => {
  const record = hostSkillTargetRecord('本机')
  assert.equal(record.id, HOST_SKILL_TARGET_ID)
  assert.equal(record.name, '本机')
  assert.equal(record.sandbox_type, 'host')
})

test('host target id is matched exactly', () => {
  assert.equal(isHostSkillTarget('host'), true)
  assert.equal(isHostSkillTarget(' host '), true)
  assert.equal(isHostSkillTarget('HOST'), false)
  assert.equal(isHostSkillTarget(''), false)
  assert.equal(isHostSkillTarget(undefined), false)
})

test('host-only mode needs host and no remote', () => {
  assert.equal(hostSkillsOnly(false, true), true)
  assert.equal(hostSkillsOnly(true, true), false)
  assert.equal(hostSkillsOnly(false, false), false)
})

test('mention target is host on lite and the agent sandbox otherwise', () => {
  assert.equal(mentionSkillTargetId(true, ''), HOST_SKILL_TARGET_ID)
  assert.equal(mentionSkillTargetId(true, 'cfg-1'), HOST_SKILL_TARGET_ID)
  assert.equal(mentionSkillTargetId(false, 'cfg-1'), 'cfg-1')
  assert.equal(mentionSkillTargetId(false, '  '), '')
  assert.equal(mentionSkillTargetId(false, undefined), '')
})
