import assert from 'node:assert/strict';
import test from 'node:test';

import {
  MODEL_SETTINGS_TAB_TYPES,
  VIDEO_MODEL_EDITOR_TYPE,
  VIDEO_MODEL_TYPE,
  countModelsOfType,
  isVideoModelType,
  videoModelCount,
} from './modelVideoTab';

test('「视频」是设置页的一个真实类型 tab，排在类型列表里', () => {
  assert.deepEqual([...MODEL_SETTINGS_TAB_TYPES], ['chat', 'embedding', 'rerank', 'vllm', 'asr', 'video']);
  assert.equal(VIDEO_MODEL_EDITOR_TYPE, 'video');
  assert.equal(VIDEO_MODEL_TYPE, 'Video');
});

test('tab 计数按真实类型统计，含 Video 行', () => {
  const models = [{ _modelType: 'video' }, { _modelType: 'video' }, { _modelType: 'asr' }];
  assert.equal(countModelsOfType(models, 'video'), 2);
  assert.equal(countModelsOfType(models, 'asr'), 1);
  assert.equal(countModelsOfType(models, 'chat'), 0);
});

test('输入模态含 video 的对话模型不算视频模型（tab 不是过滤器）', () => {
  // 这正是改造前的行为：按 capabilities.input 筛选会把一个对话模型列进「视频」
  // tab，却既不能按该类型新建、也不能按该类型删除。现在它只看行类型。
  const chatWithVideoInput = {
    _modelType: 'chat',
    type: 'KnowledgeQA',
    capabilities: { input: ['text', 'video'] },
  };
  assert.equal(countModelsOfType([chatWithVideoInput], 'video'), 0);
  assert.equal(isVideoModelType(chatWithVideoInput.type), false);
  assert.equal(videoModelCount([chatWithVideoInput]), 0);
});

test('Video 行按类型被识别，其它类型与空值都不会落进视频 tab', () => {
  assert.equal(isVideoModelType('Video'), true);
  assert.equal(isVideoModelType('video'), true);
  assert.equal(isVideoModelType(' video '), true);
  assert.equal(videoModelCount([{ type: 'Video' }, { type: 'video' }, { type: 'ASR' }]), 2);
  for (const type of ['KnowledgeQA', 'VLLM', 'Embedding', 'Rerank', 'ASR', '', null, undefined]) {
    assert.equal(isVideoModelType(type), false, `type=${String(type)}`);
  }
  assert.equal(videoModelCount([]), 0);
});
