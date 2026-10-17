import assert from 'node:assert/strict';
import test from 'node:test';

import { countVideoCapable, supportsVideoInput } from './modelVideoTab';

test('视频 tab 只列 capabilities.input 含 video 的模型', () => {
  assert.equal(supportsVideoInput({ capabilities: { input: ['text', 'image', 'video'] } }), true);
  assert.equal(supportsVideoInput({ capabilities: { input: ['video'] } }), true);
});

test('只支持图片或文字的模型不算视频模型', () => {
  assert.equal(supportsVideoInput({ capabilities: { input: ['text', 'image'] } }), false);
  assert.equal(supportsVideoInput({ capabilities: { input: ['text'] } }), false);
  // 没有 capabilities 的模型（本地模型、ASR 等）不会出现在视频 tab 里。
  assert.equal(supportsVideoInput({}), false);
  assert.equal(supportsVideoInput({ capabilities: null }), false);
  assert.equal(supportsVideoInput({ capabilities: { input: null } }), false);
});

test('视频 tab 的计数与过滤同一套判定', () => {
  const models = [
    { capabilities: { input: ['text', 'video'] } },
    { capabilities: { input: ['text', 'image'] } },
    { capabilities: { input: ['text', 'video'] } },
    {},
  ];
  assert.equal(countVideoCapable(models), 2);
});
