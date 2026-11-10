import assert from 'node:assert/strict'
import test from 'node:test'

import { agentTurnUsedSkillScript, isCollectingSkillArtifacts } from './skillArtifacts.ts'

test('agentTurnUsedSkillScript detects execute_skill_script in the stream', () => {
  assert.equal(agentTurnUsedSkillScript(undefined), false)
  assert.equal(agentTurnUsedSkillScript([]), false)
  assert.equal(agentTurnUsedSkillScript([{ tool_name: 'web_search' }]), false)
  assert.equal(agentTurnUsedSkillScript([{ tool_name: 'execute_skill_script' }]), true)
})

test('isCollectingSkillArtifacts covers the post-answer upload gap', () => {
  const stream = [{ tool_name: 'execute_skill_script' }]
  assert.equal(isCollectingSkillArtifacts({ agentEventStream: stream }), true)
  assert.equal(isCollectingSkillArtifacts({ artifactsCollecting: true }), true)
  assert.equal(
    isCollectingSkillArtifacts({ agentEventStream: stream, is_completed: true }),
    false,
  )
  assert.equal(
    isCollectingSkillArtifacts({
      agentEventStream: stream,
      artifacts: [{ file_name: 'chart.html' }],
    }),
    false,
  )
})
