import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { computed, effectScope, reactive, ref, watch } from 'vue'
import { supportedLevels, levelLabelKey, levelFromLegacy, clampLevel } from '../utils/reasoningEffort'

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const start = source.indexOf('const selectedModel = computed(')
const end = source.indexOf('// 模型展示名', start)
assert.ok(start > 0 && end > start)
const compiled = ts.transpileModule(source.slice(start, end), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture() {
  const availableModels = ref<any[]>([])
  const selectedModelId = ref('model-a')
  const settingsStore = reactive({ reasoningEffortOverride: '', _isApplyingSessionState: false })
  const currentAgentConfig = ref({ thinking: true, reasoning_effort: 'high' })
  const scope = effectScope()
  const output: any = {}
  scope.run(() => runInNewContext(compiled + '\nObject.assign(output, { reasoningLevels, displayedReasoningLevel, selectReasoningLevel });', {
    computed, ref, watch, supportedLevels, levelLabelKey, levelFromLegacy, clampLevel, currentAgentConfig, availableModels, selectedModelId, settingsStore, output, t: (key: string) => key,
  }))
  return { availableModels, selectedModelId, settingsStore, currentAgentConfig, ...output, close: () => scope.stop() }
}

test('composer reasoning follows model capabilities and discards incompatible overrides', () => {
  const f = fixture()
  try {
    assert.equal(f.reasoningLevels.value.length, 0, 'unknown model hides reasoning')
    f.availableModels.value = [
      { id: 'model-a', capabilities: { thinking_levels: ['off', 'auto', 'low', 'high'] } },
      { id: 'model-b', capabilities: { thinking_levels: ['auto', 'medium', 'max'] } },
      { id: 'model-c', capabilities: { thinking_levels: [] } },
    ]
    assert.deepEqual(Array.from(f.reasoningLevels.value), ['off', 'auto', 'low', 'high'])
    assert.equal(f.displayedReasoningLevel.value, 'high')
    assert.equal(f.settingsStore.reasoningEffortOverride, '', 'agent default stays inherited')
    f.settingsStore.reasoningEffortOverride = 'high'
    f.selectedModelId.value = 'model-b'
    assert.equal(f.settingsStore.reasoningEffortOverride, '')
    assert.deepEqual(Array.from(f.reasoningLevels.value), ['auto', 'medium', 'max'], 'always-on models offer no Off')
    f.settingsStore.reasoningEffortOverride = 'auto'
    f.selectedModelId.value = 'model-a'
    assert.equal(f.settingsStore.reasoningEffortOverride, 'auto', 'supported choices survive a model switch')
    f.selectedModelId.value = 'model-c'
    assert.equal(f.reasoningLevels.value.length, 0)
    assert.equal(f.settingsStore.reasoningEffortOverride, '')
  } finally { f.close() }
})

test('session restoration waits for model data and does not validate against the previous session model', () => {
  const f = fixture()
  try {
    f.settingsStore.reasoningEffortOverride = 'high'
    assert.equal(f.settingsStore.reasoningEffortOverride, 'high')
    f.availableModels.value = [{ id: 'model-a', capabilities: { thinking_levels: ['auto', 'high'] } }]
    assert.equal(f.settingsStore.reasoningEffortOverride, 'high')
    f.settingsStore._isApplyingSessionState = true
    f.settingsStore.reasoningEffortOverride = 'max'
    f.availableModels.value.push({ id: 'model-b', capabilities: { thinking_levels: ['auto', 'max'] } })
    f.selectedModelId.value = 'model-b'
    f.settingsStore._isApplyingSessionState = false
    assert.equal(f.settingsStore.reasoningEffortOverride, 'max')
  } finally { f.close() }
})

test('default displays the agent level without sending an override, and selection can restore inheritance', () => {
  const f = fixture()
  try {
    f.availableModels.value = [{ id: 'model-a', capabilities: { thinking_levels: ['off', 'auto', 'low', 'high'] } }]
    assert.equal(f.displayedReasoningLevel.value, 'high')
    assert.equal(f.settingsStore.reasoningEffortOverride, '')
    f.currentAgentConfig.value = { thinking: true, reasoning_effort: 'low' }
    assert.equal(f.displayedReasoningLevel.value, 'low')
    f.selectReasoningLevel('high')
    assert.equal(f.settingsStore.reasoningEffortOverride, 'high')
    assert.equal(f.displayedReasoningLevel.value, 'high')
    f.selectReasoningLevel('low')
    assert.equal(f.settingsStore.reasoningEffortOverride, '')
    f.currentAgentConfig.value = { thinking: false, reasoning_effort: '' }
    assert.equal(f.displayedReasoningLevel.value, 'off', 'legacy boolean still supplies the default')
    f.currentAgentConfig.value.thinking = true
    assert.equal(f.displayedReasoningLevel.value, 'auto')
  } finally { f.close() }
})
