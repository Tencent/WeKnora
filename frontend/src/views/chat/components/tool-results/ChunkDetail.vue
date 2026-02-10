<template>
  <div class="chunk-detail">
    <div class="info-section">
      <div class="info-field">
        <span class="field-label">{{ $t('chat.chunkIdLabel') }}</span>
        <span class="field-value"><code>{{ data.chunk_id }}</code></span>
      </div>
      <div class="info-field">
        <span class="field-label">{{ $t('chat.documentIdLabel') }}</span>
        <span class="field-value"><code>{{ data.knowledge_id }}</code></span>
      </div>
      <div class="info-field">
        <span class="field-label">{{ $t('chat.positionLabel') }}</span>
        <span class="field-value">{{ $t('chat.chunkPositionValue', { index: data.chunk_index }) }}</span>
      </div>
      <div v-if="data.content_length" class="info-field">
        <span class="field-label">{{ $t('chat.contentLengthLabelSimple') }}</span>
        <span class="field-value">{{ $t('chat.lengthChars', { value: data.content_length }) }}</span>
      </div>
    </div>

    <div class="info-section">
      <div class="info-section-title">{{ $t('chat.fullContentLabel') }}</div>
      <div class="full-content">{{ data.content }}</div>
    </div>

    <div class="info-section">
      <div class="action-buttons">
        <button class="action-button" @click="copyToClipboard">
          📋 {{ $t('chat.copyContent') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { defineProps } from 'vue';
import type { ChunkDetailData } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

const props = defineProps<{
  data: ChunkDetailData;
}>();

const { t } = useI18n();

const copyToClipboard = () => {
  const text = props.data.content;
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).catch(() => {
      fallbackCopy(text);
    });
  } else {
    fallbackCopy(text);
  }
};

function fallbackCopy(text: string) {
  const textArea = document.createElement('textarea');
  textArea.value = text;
  textArea.style.position = 'fixed';
  textArea.style.opacity = '0';
  document.body.appendChild(textArea);
  textArea.select();
  document.execCommand('copy');
  document.body.removeChild(textArea);
}
</script>

<style lang="less" scoped>
@import './tool-results.less';

.chunk-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 8px 0;
}

code {
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 11px;
  background: #f0f0f0;
  padding: 2px 4px;
  border-radius: 3px;
}

.action-buttons {
  display: flex;
  gap: 8px;
}
</style>

