import assert from 'node:assert/strict'
import test from 'node:test'

import {
  clampThinkingEffort,
  defaultThinkingControl,
  isHyReasoningEffortModel,
  isGlmReasoningEffortModel,
  resolveThinkingControl,
  thinkingEffortLevels,
} from './thinkingControl.ts'

// Cases mirror internal/models/chat/provider_test.go TestResolveProvider thinking defaults.
test('defaultThinkingControl matches backend provider adapters', () => {
  const cases: Array<[string, string, ReturnType<typeof defaultThinkingControl>]> = [
    ['generic', 'anything', 'chat_template_kwargs'],
    ['generic', 'Hy3', 'reasoning_effort'],
    ['generic', 'glm-5.3', 'reasoning_effort'],
    ['nvidia', 'Hy4-preview', 'reasoning_effort'],
    ['litellm', 'glm-5.3-flash', 'reasoning_effort'],
    ['hunyuan', 'Hy3', 'reasoning_effort'],
    ['hunyuan', 'Hy4-preview', 'reasoning_effort'],
    ['hunyuan', 'tencent/Hy4-preview-FP8', 'reasoning_effort'],
    ['nvidia', 'anything', 'chat_template_kwargs'],
    ['litellm', 'anything', 'chat_template_kwargs'],
    ['volcengine', 'doubao', 'thinking_type'],
    ['aliyun', 'qwen3-32b', 'enable_thinking'],
    ['aliyun', 'qwen-plus', 'enable_thinking'],
    ['aliyun', 'gpt-4', 'none'],
    ['lkeap', '', 'thinking_type'],
    ['lkeap', 'deepseek-v3.1', 'thinking_type'],
    ['lkeap', 'deepseek-r1', 'none'],
    ['openai', 'gpt-4o', 'none'],
    ['openai', 'gpt-5', 'none'],
    ['azure_openai', 'gpt-4', 'none'],
    ['deepseek', 'deepseek-chat', 'none'],
    ['zhipu', 'glm-4', 'none'],
    ['zhipu', 'glm-5.3', 'glm_reasoning_effort'],
    ['zhipu', 'glm-5.3-flash', 'glm_reasoning_effort'],
    ['zhipu', 'glm-5', 'glm_reasoning_effort'],
    ['gemini', 'gemini-2.0', 'none'],
    ['siliconflow', 'qwen3-8b', 'none'],
    ['hunyuan', 'hunyuan-turbo', 'none'],
    ['moonshot', 'moonshot-v1-8k', 'none'],
    ['weknoracloud', 'anything', 'none'],
  ]
  for (const [provider, model, want] of cases) {
    assert.equal(
      defaultThinkingControl(provider, model),
      want,
      `${provider}/${model}`,
    )
  }
})

test('isHyReasoningEffortModel only matches Hy3 and Hy4 families', () => {
  assert.equal(isHyReasoningEffortModel('Hy3'), true)
  assert.equal(isHyReasoningEffortModel('tencent/Hy4-preview-FP8'), true)
  assert.equal(isHyReasoningEffortModel('hunyuan-turbo'), false)
  assert.equal(isHyReasoningEffortModel('hypertension-model'), false)
})

test('reasoning_effort control is honored on any provider', () => {
  assert.equal(resolveThinkingControl('reasoning_effort', 'hunyuan', 'Hy4-preview'), 'reasoning_effort')
  assert.equal(resolveThinkingControl('reasoning_effort', 'generic', 'Hy4-preview'), 'reasoning_effort')
  assert.equal(resolveThinkingControl('reasoning_effort', 'hunyuan', 'hunyuan-turbo'), 'reasoning_effort')
  assert.equal(resolveThinkingControl('reasoning_effort', 'generic', 'qwen3'), 'reasoning_effort')
})

test('glm_reasoning_effort control is limited to Zhipu GLM-5.x models', () => {
  assert.equal(resolveThinkingControl('glm_reasoning_effort', 'zhipu', 'glm-5.3'), 'glm_reasoning_effort')
  assert.equal(resolveThinkingControl('glm_reasoning_effort', 'zhipu', 'glm-4'), 'none')
  assert.equal(resolveThinkingControl('glm_reasoning_effort', 'generic', 'glm-5.3'), 'reasoning_effort')
})

test('isGlmReasoningEffortModel only matches GLM-5.x family', () => {
  assert.equal(isGlmReasoningEffortModel('glm-5.3'), true)
  assert.equal(isGlmReasoningEffortModel('glm-5.3-flash'), true)
  assert.equal(isGlmReasoningEffortModel('glm-5'), true)
  assert.equal(isGlmReasoningEffortModel('glm-4'), false)
  assert.equal(isGlmReasoningEffortModel('zhipu/glm-5.3'), true)
})

// Level sets mirror the effortStrategy definitions in thinking.go.
test('thinkingEffortLevels matches the backend level sets', () => {
  assert.deepEqual(thinkingEffortLevels('hunyuan', 'Hy3'), ['no_think', 'low', 'high'])
  assert.deepEqual(thinkingEffortLevels('hunyuan', 'Hy4-preview'), ['no_think', 'high'])
  assert.deepEqual(thinkingEffortLevels('generic', 'Hy4-preview'), ['no_think', 'high'])
  assert.deepEqual(thinkingEffortLevels('zhipu', 'glm-5.3'), ['low', 'high', 'max'])
  assert.deepEqual(thinkingEffortLevels('generic', 'glm-5.3'), ['no_think', 'low', 'high', 'max'])
  assert.deepEqual(
    thinkingEffortLevels('generic', 'qwen3', 'reasoning_effort'),
    ['no_think', 'low', 'high', 'max'],
  )
  assert.equal(thinkingEffortLevels('zhipu', 'glm-4'), null)
  assert.equal(thinkingEffortLevels('hunyuan', 'hunyuan-turbo'), null)
  assert.equal(thinkingEffortLevels('openai', 'gpt-4o'), null)
})

test('clampThinkingEffort snaps a stale level onto the new model', () => {
  const hy3 = ['no_think', 'low', 'high'] as const
  const hy4 = ['no_think', 'high'] as const
  const glm = ['low', 'high', 'max'] as const

  // Valid levels pass through untouched.
  assert.equal(clampThinkingEffort('low', hy3), 'low')
  assert.equal(clampThinkingEffort('max', glm), 'max')

  // GLM cannot express no_think; the lightest level stands in, matching the
  // no_think -> low alias in glmReasoningEffort.
  assert.equal(clampThinkingEffort('no_think', glm), 'low')
  // Hy has no max level.
  assert.equal(clampThinkingEffort('max', hy3), 'high')
  assert.equal(clampThinkingEffort('max', hy4), 'high')
  // Hy4 dropped low; a tie between no_think and high rounds up.
  assert.equal(clampThinkingEffort('low', hy4), 'high')
  // Unknown or empty input falls back to the strongest level.
  assert.equal(clampThinkingEffort('medium', glm), 'max')
  assert.equal(clampThinkingEffort(undefined, hy3), 'high')
})
