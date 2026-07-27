import assert from 'node:assert/strict'
import test from 'node:test'
import { selectedSkillKeys, skillReferenceKey, skillReferencesFromKeys } from './agentSkillSelection.ts'

test('hydrates canonical skill references after an agent is reloaded', () => {
  assert.deepEqual(selectedSkillKeys([
    { source: 'preloaded', skill_id: '查案例' },
    { source: 'tenant', skill_id: 'skill-id' },
  ]), ['preloaded:查案例', 'tenant:skill-id'])
})

test('hydrates legacy selected skill names as preloaded references', () => {
  assert.deepEqual(selectedSkillKeys([], ['查案例']), ['preloaded:查案例'])
})

test('serializes checkbox keys back to deduplicated canonical references', () => {
  assert.deepEqual(skillReferencesFromKeys(['tenant:skill-id', 'preloaded:查案例', 'tenant:skill-id']), [
    { source: 'tenant', skill_id: 'skill-id' },
    { source: 'preloaded', skill_id: '查案例' },
  ])
  assert.equal(skillReferenceKey({ source: 'tenant', skill_id: 'skill-id', name: '企业法务', description: '' }), 'tenant:skill-id')
})
