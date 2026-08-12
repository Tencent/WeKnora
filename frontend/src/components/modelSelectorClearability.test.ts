import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const selector = readFileSync(new URL('./ModelSelector.vue', import.meta.url), 'utf8')
const agentEditor = readFileSync(new URL('../views/agent/AgentEditorModal.vue', import.meta.url), 'utf8')
const kbModelConfig = readFileSync(new URL('../views/knowledge/settings/KBModelConfig.vue', import.meta.url), 'utf8')
const kbEditor = readFileSync(new URL('../views/knowledge/KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')
const uploadConfirm = readFileSync(new URL('../views/knowledge/components/UploadConfirmDialog.vue', import.meta.url), 'utf8')

function modelSelectorTag(source: string, selectedModelBinding: string): string {
  const tag = source
    .match(/<ModelSelector\b[\s\S]*?\/>/g)
    ?.find((candidate) => candidate.includes(selectedModelBinding))
  assert.ok(tag, `找不到模型选择器：${selectedModelBinding}`)
  return tag
}

function assertClearable(tag: string): void {
  assert.match(tag, /(?:\s|:)clearable(?:\s|=|\/>)/)
}

function assertNotClearable(tag: string): void {
  assert.doesNotMatch(tag, /(?:\s|:)clearable(?:\s|=|\/>)/)
}

test('ModelSelector 清空时向父组件回传空字符串，默认仍不可清空', () => {
  assert.match(selector, /:clearable="clearable"/)
  assert.match(selector, /clearable:\s*false/)
  assert.match(selector, /emit\('update:selectedModelId', value \|\| ''\)/)
})

test('智能体中允许继承或关闭的可选模型可以恢复为空', () => {
  const rerank = modelSelectorTag(agentEditor, 'formData.config.rerank_model_id')
  assert.match(rerank, /:clearable="!needsRerankModel"/)
  assertClearable(modelSelectorTag(agentEditor, 'formData.config.query_understand_model_id'))
  assertClearable(modelSelectorTag(agentEditor, 'formData.config.asr_model_id'))
  assertClearable(modelSelectorTag(agentEditor, 'formData.config.question_suggestions.follow_ups.model_id'))
})

test('知识库仅在模型确实可选时允许恢复为空', () => {
  const embedding = modelSelectorTag(kbModelConfig, 'config.embeddingModelId')
  assert.match(embedding, /:clearable="ragEnabled === false && wikiEnabled"/)
  assertClearable(modelSelectorTag(kbModelConfig, 'config.wikiSynthesisModelId'))
})

test('必填模型继续保持不可清空', () => {
  assertNotClearable(modelSelectorTag(agentEditor, 'formData.config.model_id'))
  assertNotClearable(modelSelectorTag(agentEditor, 'formData.config.vlm_model_id'))
  assertNotClearable(modelSelectorTag(kbModelConfig, 'config.llmModelId'))
  assertNotClearable(modelSelectorTag(kbEditor, 'formData.multimodalConfig.vllmModelId'))
  assertNotClearable(modelSelectorTag(kbEditor, 'formData.asrConfig.modelId'))
  assertNotClearable(modelSelectorTag(uploadConfirm, 'uiState.multimodalConfig.vllmModelId'))
  assertNotClearable(modelSelectorTag(uploadConfirm, 'uiState.asrConfig.modelId'))
})
