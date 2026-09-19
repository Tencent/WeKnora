import assert from 'node:assert/strict'
import test from 'node:test'
import { getAgentToolIconName } from './agent-tool-icons.ts'
import { collectSkillCardGroups, registryOfSource } from './skillInstallCards.ts'

const searchEvent = (id, candidates, extra = {}) => ({
  type: 'tool_call',
  tool_name: 'search_skills',
  tool_call_id: id,
  success: true,
  tool_data: {
    display_type: 'skill_candidates',
    sandbox_config_id: 'cfg-1',
    candidates,
    ...extra,
  },
})

const shellEvent = (id, load, extra = {}) => ({
  type: 'tool_call',
  tool_name: 'shell_exec',
  tool_call_id: id,
  success: true,
  tool_data: { display_type: 'shell_exec', skill_temporary_load: load, ...extra },
})

test('search results become one card group per call, targeting the run sandbox', () => {
  const groups = collectSkillCardGroups([
    { type: 'thinking', content: 'x' },
    searchEvent('c1', [
      { name: 'pdf', source: 'skills-sh:anthropics/skills/pdf', registry: 'skills-sh' },
      { name: '', source: 'broken' },
      { name: 'ppt', source: '@ivan/ppt' },
    ], { agent_selects_skills: true }),
  ])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].key, 'c1')
  assert.equal(groups[0].sandboxConfigId, 'cfg-1')
  assert.equal(groups[0].agentSelectsSkills, true)
  assert.equal(groups[0].newSessionsOnly, false)
  assert.equal(groups[0].temporary, false)
  assert.deepEqual(groups[0].candidates.map((c) => c.source), ['skills-sh:anthropics/skills/pdf', '@ivan/ppt'],
    'a candidate without a name or source cannot be installed and is dropped')
})

test('pending or failed calls and ordinary shell commands produce no cards', () => {
  const groups = collectSkillCardGroups([
    { ...searchEvent('c1', [{ name: 'pdf', source: 'pdf' }]), pending: true },
    { ...searchEvent('c2', [{ name: 'pdf', source: 'pdf' }]), success: false },
    { type: 'tool_call', tool_name: 'shell_exec', success: true, tool_data: { display_type: 'shell_exec' } },
  ])
  assert.deepEqual(groups, [])
})

test('a skill loaded by a command is offered as a temporary card with its source', () => {
  const [group] = collectSkillCardGroups([
    shellEvent('s1', {
      tool: 'skills', source: 'skills-sh:anthropics/skills/pdf', name: 'pdf', sandbox_config_id: 'cfg-1',
      new_sessions_only: true, agent_selects_skills: true,
    }),
  ])
  assert.equal(group.temporary, true)
  assert.equal(group.newSessionsOnly, true, 'the card must not promise this conversation the skill')
  assert.equal(group.agentSelectsSkills, true)
  assert.equal(group.temporaryName, 'pdf')
  assert.equal(group.sandboxConfigId, 'cfg-1')
  assert.deepEqual(group.candidates, [
    { name: 'pdf', source: 'skills-sh:anthropics/skills/pdf', registry: 'skills-sh' },
  ])
})

test('the temporary notice still shows when no source could be derived', () => {
  const [group] = collectSkillCardGroups([shellEvent('s1', { tool: 'hermes' })])
  assert.equal(group.temporary, true)
  assert.equal(group.temporaryName, undefined)
  assert.deepEqual(group.candidates, [])
  assert.equal(group.sandboxConfigId, '', 'without search_skills there is no install target')
})

test('the same skill is offered once per sandbox across search and command cards', () => {
  const groups = collectSkillCardGroups([
    shellEvent('s1', { tool: 'skills', source: 'skills-sh:a/b/pdf', name: 'pdf', sandbox_config_id: 'cfg-1' }),
    searchEvent('c1', [
      { name: 'pdf', source: 'skills-sh:a/b/pdf' },
      { name: 'docx', source: 'skills-sh:a/b/docx' },
    ]),
    shellEvent('s2', { tool: 'skills', source: 'skills-sh:a/b/docx', name: 'docx', sandbox_config_id: 'cfg-1' }),
    searchEvent('c2', [{ name: 'pdf', source: 'skills-sh:a/b/pdf' }]),
  ])
  assert.deepEqual(groups.map((g) => [g.key, g.candidates.map((c) => c.name)]), [
    ['s1', ['pdf']],
    ['c1', ['docx']],
  ])
})

test('registryOfSource labels the locators the backend derives', () => {
  assert.equal(registryOfSource('skills-sh:anthropics/skills/pdf'), 'skills-sh')
  assert.equal(registryOfSource('@ivan/ppt'), 'clawhub')
  assert.equal(registryOfSource('pdf-tools'), 'clawhub')
  assert.equal(registryOfSource('https://github.com/o/r'), 'github')
  assert.equal(registryOfSource('https://raw.githubusercontent.com/o/r/main/x/SKILL.md'), 'github')
  assert.equal(registryOfSource('https://skillhub.cn/skills/ppt'), 'skillhub')
  assert.equal(registryOfSource('https://example.com/skill.zip'), 'url')
})

test('search_skills has its own step icon', () => {
  assert.equal(getAgentToolIconName('search_skills'), 'app')
})
