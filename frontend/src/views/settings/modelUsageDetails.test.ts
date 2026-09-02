import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./ModelSettings.vue', import.meta.url), 'utf8')
const agentListSource = readFileSync(new URL('../agent/AgentList.vue', import.meta.url), 'utf8')
const knowledgeBaseEditorSource = readFileSync(
  new URL('../knowledge/KnowledgeBaseEditorModal.vue', import.meta.url),
  'utf8',
)
const agentEditorSource = readFileSync(new URL('../agent/AgentEditorModal.vue', import.meta.url), 'utf8')

test('model deletion renders structured usage groups and localized fallback errors', () => {
  assert.match(source, /error instanceof ModelInUseError/)
  assert.match(source, /usageConflict\.knowledge_bases/)
  assert.match(source, /usageConflict\.agents/)
  assert.match(source, /usageConflict\.long_term_memory\.bindings/)
  assert.match(source, /MessagePlugin\.error\(t\('modelSettings\.toasts\.deleteFailed'\)\)/)
  assert.doesNotMatch(
    source,
    /MessagePlugin\.error\(error\.message \|\| t\('modelSettings\.toasts\.deleteFailed'\)\)/,
  )
})

test('referenced knowledge bases and agents open their relevant configuration pages', () => {
  assert.match(source, /openUsageResource\('knowledge_base', resource\.id, resource\.bindings\)/)
  assert.match(source, /openUsageResource\('agent', resource\.id\)/)
  assert.match(source, /uiStore\.closeKBEditor\(\)/)
  assert.match(source, /uiStore\.closeSettings\(\)/)
  assert.match(source, /await nextTick\(\)/)
  assert.match(
    source,
    /const routeName = router\.currentRoute\.value\.name[\s\S]*canFocusExistingKnowledgeBase[\s\S]*KNOWLEDGE_BASE_EDITOR_HOST_ROUTES\.has\(routeName\)[\s\S]*focusKbEditorSection\(knowledgeBaseSection\)/,
  )
  assert.match(source, /await router\.push\(modelUsageResourceRoute\(kind, id\)\)/)
  assert.match(source, /uiStore\.openKBSettings\(id, knowledgeBaseSection\)/)
  assert.match(
    agentListSource,
    /editingAgent\.value\?\.id === agent\.id[\s\S]*focusAgentEditorSection\(requestedSection\)[\s\S]*editorVisible\.value = false\s*await nextTick\(\)\s*if \(generation !== editOpenGeneration\) return[\s\S]*editorVisible\.value = true/,
  )
  assert.match(agentListSource, /const agent = resolveAgentForEdit\([\s\S]*if \(!agent\) return[\s\S]*router\.replace/)
  assert.match(agentEditorSource, /v-if="editorInitializing"[\s\S]*:disabled="editorInitializing"/)
  assert.match(agentEditorSource, /generation !== editorInitializationGeneration \|\| !props\.visible/)
  assert.match(knowledgeBaseEditorSource, /v-if="loading"[\s\S]*:disabled="loading"/)
  assert.match(knowledgeBaseEditorSource, /isCurrentKBLoad\(generation, kbId\)/)
  assert.match(knowledgeBaseEditorSource, /generation !== kbEditorLoadGeneration \|\| !props\.visible/)
  assert.match(knowledgeBaseEditorSource, /setTimeout\(\(\) => \{\s*if \(props\.visible\) return\s*resetState\(\)/)
})
