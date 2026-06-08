import assert from 'node:assert/strict'
import test from 'node:test'

import { defaultThinkingControl } from './thinkingControl.ts'

// Cases mirror internal/models/chat/provider_test.go TestResolveProvider thinking defaults.
test('defaultThinkingControl matches backend provider adapters', () => {
  const cases: Array<[string, string, ReturnType<typeof defaultThinkingControl>]> = [
    ['generic', 'anything', 'chat_template_kwargs'],
    ['nvidia', 'anything', 'chat_template_kwargs'],
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
