import assert from 'node:assert/strict';
import test from 'node:test';

import {
  isVideoAudioModelConfigured,
  isVideoFrameModelConfigured,
} from './videoConfig';

test('视频 ASR 只看视频页自己的 modelId', () => {
  assert.equal(isVideoAudioModelConfigured({ modelId: 'asr-video' }), true);
  assert.equal(isVideoAudioModelConfigured({ modelId: '  asr-video  ' }), true);
});

test('视频 ASR 为空就是没配，不回退到「音频处理」', () => {
  // 有 asr_config 但视频页没选模型时必须为 false：函数根本读不到音频配置，
  // 这里用带多余字段的对象模拟调用方传进来的整份 formData。
  const withAudioFallbackData = {
    modelId: '',
    asrConfig: { enabled: true, modelId: 'asr-audio' },
  };
  assert.equal(isVideoAudioModelConfigured(withAudioFallbackData), false);

  assert.equal(isVideoAudioModelConfigured({ modelId: '   ' }), false);
  assert.equal(isVideoAudioModelConfigured({}), false);
  assert.equal(isVideoAudioModelConfigured(null), false);
  assert.equal(isVideoAudioModelConfigured(undefined), false);
});

test('视频画面 VLM 只看视频页自己的 vlm.modelId', () => {
  assert.equal(isVideoFrameModelConfigured({ vlm: { modelId: 'vlm-video' } }), true);
  assert.equal(isVideoFrameModelConfigured({ vlm: { modelId: '  vlm-video  ' } }), true);
});

test('画面 VLM 为空就是没配，不回退到「图像处理」', () => {
  // 「图像处理」页配好了 VLM 也不能让视频页显示已配置：函数根本读不到那份
  // 配置，这里用带多余字段的对象模拟调用方传进来的整份 formData。
  const withImageFallbackData = {
    vlm: { modelId: '' },
    multimodalConfig: { enabled: true, vllmModelId: 'vlm-image' },
  };
  assert.equal(isVideoFrameModelConfigured(withImageFallbackData), false);

  assert.equal(isVideoFrameModelConfigured({ vlm: { modelId: '   ' } }), false);
  assert.equal(isVideoFrameModelConfigured({ vlm: null }), false);
  assert.equal(isVideoFrameModelConfigured({}), false);
  assert.equal(isVideoFrameModelConfigured(null), false);
  assert.equal(isVideoFrameModelConfigured(undefined), false);
});
