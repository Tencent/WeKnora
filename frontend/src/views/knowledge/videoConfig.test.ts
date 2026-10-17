import assert from 'node:assert/strict';
import test from 'node:test';

import {
  isVideoAudioModelConfigured,
  isVideoDirectModelConfigured,
  isVideoFrameModelConfigured,
  normalizeVideoDirectInput,
  videoDirectInputStoresWithVendor,
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

test('视频直读只看视频页自己的 videoModelId', () => {
  assert.equal(isVideoDirectModelConfigured({ videoModelId: 'video-1' }), true);
  assert.equal(isVideoDirectModelConfigured({ videoModelId: '  video-1  ' }), true);
});

test('直读没配视频模型就是没配，不回退到 ASR / VLM', () => {
  // 切到 video_direct 时旧档位的 modelId / vlm 可能还留着；它们只用于失败后的
  // 降级，不能让「已配置」变成 true，否则页面会以为直读可用。
  const switched = {
    videoModelId: '',
    modelId: 'asr-video',
    vlm: { modelId: 'vlm-video' },
  };
  assert.equal(isVideoDirectModelConfigured(switched), false);

  assert.equal(isVideoDirectModelConfigured({ videoModelId: '   ' }), false);
  assert.equal(isVideoDirectModelConfigured({}), false);
  assert.equal(isVideoDirectModelConfigured(null), false);
  assert.equal(isVideoDirectModelConfigured(undefined), false);
});

test('直读传输方式为空或未知时读作 auto，与后端归一一致', () => {
  // 老行没有 direct_input；Typo 不能变成客户端不认识的 switch 分支。
  assert.equal(normalizeVideoDirectInput(), 'auto');
  assert.equal(normalizeVideoDirectInput(''), 'auto');
  assert.equal(normalizeVideoDirectInput('   '), 'auto');
  assert.equal(normalizeVideoDirectInput('auto'), 'auto');
  assert.equal(normalizeVideoDirectInput(' base64 '), 'base64');
  assert.equal(normalizeVideoDirectInput('upload'), 'upload');
  assert.equal(normalizeVideoDirectInput('url'), 'url');
  assert.equal(normalizeVideoDirectInput('BASE64'), 'auto');
  assert.equal(normalizeVideoDirectInput('stream'), 'auto');
  assert.equal(normalizeVideoDirectInput(null), 'auto');
  assert.equal(normalizeVideoDirectInput(undefined), 'auto');
});

test('只有 auto / upload 会把视频交给厂商的临时存储', () => {
  // 合规提示按这个判断：base64 承诺不落第三方暂存，url 由调用方自己托管。
  assert.equal(videoDirectInputStoresWithVendor('auto'), true);
  assert.equal(videoDirectInputStoresWithVendor(''), true);
  assert.equal(videoDirectInputStoresWithVendor('upload'), true);
  assert.equal(videoDirectInputStoresWithVendor('base64'), false);
  assert.equal(videoDirectInputStoresWithVendor('url'), false);
  assert.equal(videoDirectInputStoresWithVendor('unknown'), true);
});
