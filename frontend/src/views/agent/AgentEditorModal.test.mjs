import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./AgentEditorModal.vue', import.meta.url), 'utf8')

test('editing an agent closes the editor after a successful save', () => {
  assert.match(
    source,
    /await updateAgent\(formData\.value\.id, formData\.value\);\s*MessagePlugin\.success\(t\('agent\.messages\.updated'\)\);\s*emit\('success'\);\s*handleClose\(\);/
  )
})

test('the first successful create stays open for integration setup', () => {
  const createBranch = source.match(
    /if \(editorMode\.value === 'create'\) \{([\s\S]*?)^\s{4}\} else \{/m
  )?.[1]

  assert.ok(createBranch, 'expected to find the create branch')
  assert.doesNotMatch(createBranch, /handleClose\(\)/)
  assert.match(createBranch, /savedAgent\.value = created;/)
})

test('save button labels distinguish create from save-and-close', () => {
  assert.match(
    source,
    /const saveButtonLabel = computed\(\(\) =>\s*editorMode\.value === 'create'\s*\? t\('agent\.editor\.buttons\.create'\)\s*: t\('agent\.editor\.buttons\.saveAndClose'\)\s*\)/
  )
  assert.match(source, /saveButtonLabel/)
})

test('shows a post-create hint after the first successful save', () => {
  assert.match(source, /const isPostCreateSession = computed\(\(\) => !!savedAgent\.value\)/)
  assert.match(source, /settings-footer-note/)
  assert.match(source, /agent\.editor\.postCreateHint\.title/)
})

// Locate a settings row by the i18n key of its label and return the attributes
// that sit before class="setting-row" — i.e. whatever v-if guards that row.
// Anchoring on the label keeps these assertions stable across reformatting.
function settingRowGuard(labelKey) {
  const key = labelKey.replace(/\./g, '\\.')
  const row = source.match(
    new RegExp(
      `<div([^>]*)class="setting-row">\\s*<div class="setting-info">\\s*<label>\\{\\{ \\$t\\('${key}'\\) \\}\\}`
    )
  )
  assert.ok(row, `expected to find the ${labelKey} row`)
  return row[1]
}

test('conversation settings stay reachable in smart-reasoning mode', () => {
  // The agent path reads history_turns (session_agent_qa.go -> LoadAgentHistory),
  // so the section must not be gated on the running mode.
  assert.match(source, /v-show="currentSection === 'conversation'"/)
  assert.doesNotMatch(source, /currentSection === 'conversation' && !isAgentMode/)

  // The nav entry is pushed unconditionally; a re-introduced guard would indent it.
  const navItems = source.match(/const navItems = computed\(\(\) => \{([\s\S]*?)^\}\);/m)?.[1]
  assert.ok(navItems, 'expected to find the nav items computed')
  assert.match(navItems, /^  items\.push\(\{ key: 'conversation'/m)

  // Switching to agent mode must not evict the user from the section.
  // Tolerates the watch being dropped entirely by a later cleanup.
  const modeWatch = source.match(/watch\(isAgentMode, \(isAgent\) => \{([\s\S]*?)^\}\);/m)?.[1] ?? ''
  assert.doesNotMatch(modeWatch, /conversation/)
})

test('history turns is editable in smart-reasoning mode', () => {
  // A new agent defaults to smart-reasoning with multi_turn_enabled=false in the
  // form, while the server forces multi-turn on. Gating the input on the local
  // switch alone would hide it exactly where it is needed.
  const guard = settingRowGuard('agent.editor.historyTurns')
  assert.match(guard, /formData\.config\.multi_turn_enabled \|\| isAgentMode/)
})

test('multi-turn switch stays hidden in smart-reasoning mode', () => {
  // CustomAgent.EnsureDefaults pins MultiTurnEnabled to true for smart-reasoning,
  // so an editable switch would silently revert after save.
  const guard = settingRowGuard('agent.editor.multiTurn')
  assert.match(guard, /v-if="!isAgentMode"/)
})

test('query rewrite stays hidden in smart-reasoning mode', () => {
  // enable_rewrite is consumed by the KnowledgeQA pipeline only.
  const guard = settingRowGuard('agent.editor.enableRewrite')
  assert.match(guard, /!isAgentMode/)
})

test('the section description matches what the mode actually shows', () => {
  // The default copy mentions query rewriting, which agent mode does not show.
  assert.match(source, /isAgentMode\.value\s*\?\s*t\('agentEditor\.desc\.conversationSectionAgent'\)/)
  assert.match(source, /<p class="section-description">\{\{ conversationSectionDesc \}\}<\/p>/)
})

test('history turns can be raised beyond the old 20 cap', () => {
  // A 5-to-20 range sits two orders of magnitude below the 200k token budget
  // the agent engine already manages, so the turn cap always bites first.
  const input = source.match(/<t-input-number v-model="formData\.config\.history_turns"[^>]*>/)
  assert.ok(input, 'expected to find the history turns input')
  assert.match(input[0], /:max="100"/)
})

test('retrieval retention is offered to agents that actually have a knowledge base', () => {
  // retain_retrieval_history is read only by the agent path
  // (internal/agent/observe.go) and only rewrites KB/Wiki tool results, so it
  // is meaningless without a knowledge base.
  const guard = settingRowGuard('agent.editor.retainRetrievalHistory')
  assert.match(guard, /isAgentMode && hasKnowledgeBase/)
})

test('skills and sandbox share one editor section', () => {
  const navItems = source.match(/const navItems = computed\(\(\) => \{([\s\S]*?)^\}\);/m)?.[1]
  assert.ok(navItems, 'expected to find the nav items computed')
  assert.match(navItems, /key: 'skills'/)
  assert.match(navItems, /icon: SKILL_ICON/)
  assert.doesNotMatch(navItems, /key: 'sandbox'/)

  const capabilityGroup = source.match(/pickItems\(\['multimodal', 'tools', 'mcp', 'skills'\]\)/)
  assert.ok(capabilityGroup, 'expected the capability group to list skills without a separate sandbox tab')

  assert.match(source, /v-show="currentSection === 'skills' && isAgentMode"/)
  assert.doesNotMatch(source, /currentSection === 'sandbox' && isAgentMode/)
  assert.match(source, /sandbox: 'skills'/)
  assert.match(source, /formData\.config\.sandbox_config_id/)
  assert.match(source, /:disabled="!canEnableSkills"/)
  assert.match(source, /sandbox-option/)
  assert.doesNotMatch(source, /skill-info-box/)
})

test('agent skill picker uses the catalog and only enables ready installs', () => {
  assert.match(source, /function autoBindSoleSandbox\(/)
  assert.match(source, /canEnableSkills/)
  assert.match(source, /catalogSkillRows/)
  assert.match(source, /showCatalogSkillList/)
  assert.match(source, /skillsSelectionMode\.value !== 'none'/)
  assert.match(source, /:disabled="!skill\.selectable"/)
  assert.match(source, /catalogSkillGroups/)
  assert.match(source, /installPartial/)
  assert.match(source, /installCatalogToCurrent/)
  assert.match(source, /agent\.editor\.installToThisSandbox/)
  assert.match(source, /skill-pick-list/)
  assert.match(source, /skill-pick-group/)
  assert.match(source, /skill-pick__badge/)
  assert.match(source, /skillStatusIcon/)
  assert.match(source, /isSkillBusy/)
  assert.match(source, /viewInstallProgress/)
  assert.match(source, /openSkillInstallProgress/)
  assert.doesNotMatch(source, /await openSkillInstallProgress\(skill\)/)
  assert.match(source, /SandboxSkillsPanel/)
  assert.match(source, /focus-skill-id/)
  assert.match(source, /skillsGroupUnavailable/)
  assert.match(source, /selectedSandboxSummary/)
  assert.match(source, /line-clamp: 2/)
  assert.doesNotMatch(source, /skillsSelectionMode === 'selected' && catalogSkillRows/)
  assert.doesNotMatch(source, /skill-list-summary/)
  assert.doesNotMatch(source, /skill-ready-stat/)
})
