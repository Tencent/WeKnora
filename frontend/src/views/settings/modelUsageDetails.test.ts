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
  assert.match(source, /MessagePlugin\.error\(error\.message \|\| t\('modelSettings\.toasts\.deleteFailed'\)\)/)
})

test('referenced knowledge bases and agents open their relevant configuration pages', () => {
  assert.match(source, /openUsageResource\('knowledge_base', resource\.id, resource\.bindings\)/)
  assert.match(source, /openUsageResource\('agent', resource\.id, resource\.bindings\)/)
  assert.match(source, /uiStore\.closeKBEditor\(\)/)
  assert.match(source, /uiStore\.closeSettings\(\)/)
  assert.match(source, /await nextTick\(\)/)
  assert.match(
    source,
    /const routeName = router\.currentRoute\.value\.name[\s\S]*canFocusExistingKnowledgeBase[\s\S]*KNOWLEDGE_BASE_EDITOR_HOST_ROUTES\.has\(routeName\)[\s\S]*focusKbEditorSection\(knowledgeBaseSection\)/,
  )
  assert.match(source, /await router\.push\(modelUsageResourceRoute\(kind, id, bindings\)\)/)
  assert.match(source, /uiStore\.openKBSettings\(id, knowledgeBaseSection\)/)
  assert.match(
    agentListSource,
    /redirectLegacyEditQuery[\s\S]*path: `\/platform\/agents\/\$\{editId\}`/,
  )
  assert.match(
    agentListSource,
    /const redirectLegacyEditQuery = \(\) => \{[\s\S]*router\.replace\(\{\s*path: `\/platform\/agents\/\$\{editId\}`/,
  )
  // TreeRAG uses a dedicated agent editor page; modal keeps isInitializing,
  // not upstream's editorInitializing overlay flag.
  assert.match(agentEditorSource, /isInitializing\.value = true/)
  assert.match(agentEditorSource, /isInitializing\.value = false/)
  assert.match(knowledgeBaseEditorSource, /v-if="loading"[\s\S]*:disabled="loading"/)
  assert.match(knowledgeBaseEditorSource, /isCurrentKBLoad\(generation, kbId\)/)
  assert.match(knowledgeBaseEditorSource, /generation !== kbEditorLoadGeneration \|\| !props\.visible/)
  assert.match(knowledgeBaseEditorSource, /setTimeout\(\(\) => \{\s*if \(props\.visible\) return\s*resetState\(\)/)
})
