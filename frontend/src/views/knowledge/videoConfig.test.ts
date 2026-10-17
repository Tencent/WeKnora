import assert from 'node:assert/strict';
import test from 'node:test';

import { isVideoAudioModelConfigured } from './videoConfig';

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
