<template>
  <div class="document-info">

    <div v-if="documents.length" class="documents-list">
      <div
        v-for="(doc, index) in documents"
        :key="doc.knowledge_id || index"
        class="result-card document-card"
      >
        <div class="result-header document-header">
          <div class="result-title">
            <span class="doc-index">#{{ index + 1 }}</span>
            <span class="doc-title">{{ doc.title || $t('chat.notProvided') }}</span>
          </div>
          <div class="result-meta">
            <span class="meta-chip" v-if="doc.chunk_count">
              {{ $t('chat.chunkCountValue', { count: doc.chunk_count }) }}
            </span>
          </div>
        </div>
        <div class="result-content expanded">
          <div class="info-section">
            <div class="info-field">
              <span class="field-label">{{ $t('chat.documentIdLabel') }}</span>
              <span class="field-value"><code>{{ doc.knowledge_id }}</code></span>
            </div>
            <div class="info-field" v-if="doc.description">
              <span class="field-label">{{ $t('chat.documentDescriptionLabel') }}</span>
              <span class="field-value">{{ doc.description }}</span>
            </div>
            <div class="info-field" v-if="doc.source || doc.type">
              <span class="field-label">{{ $t('chat.documentSourceLabel') }}</span>
              <span class="field-value">{{ formatSource(doc) }}</span>
            </div>
            <div class="info-field" v-if="doc.channel && doc.channel !== 'web'">
              <span class="field-label">{{ $t('knowledgeBase.channelLabel') }}</span>
              <span class="field-value">{{ getChannelLabel(doc.channel) }}</span>
            </div>
            <div class="info-field" v-if="doc.file_name || doc.file_type || doc.file_size">
              <span class="field-label">{{ $t('chat.documentFileLabel') }}</span>
              <span class="field-value">
                <span v-if="doc.file_name">{{ doc.file_name }}</span>
                <template v-if="doc.file_type">&nbsp;({{ doc.file_type }})</template>
                <template v-if="doc.file_size">&nbsp;· {{ formatFileSize(doc.file_size) }}</template>
              </span>
            </div>
          </div>

          <div
            v-if="doc.metadata && Object.keys(doc.metadata).length"
            class="info-section metadata-section"
          >
            <div class="info-section-title">{{ $t('chat.documentMetadataLabel') }}</div>
            <ul class="metadata-list">
              <li
                v-for="(value, key) in doc.metadata"
                :key="`${doc.knowledge_id}-${key}`"
              >
                <span class="metadata-key">{{ key }}:</span>
                <span class="metadata-value">{{ formatMetadataValue(value) }}</span>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.documentInfoEmpty') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineProps } from 'vue';
import { useI18n } from 'vue-i18n';
import type { DocumentInfoData, DocumentInfoDocument } from '@/types/tool-results';

const props = defineProps<{
  data: DocumentInfoData;
}>();

const { t } = useI18n();

const documents = computed(() => props.data?.documents ?? []);
const errors = computed(() => props.data?.errors?.filter(Boolean) ?? []);
const totalChunkCount = computed(() =>
  documents.value.reduce((sum, doc) => sum + (doc.chunk_count || 0), 0),
);

const channelLabelMap: Record<string, string> = {
  web: 'knowledgeBase.channelWeb',
  api: 'knowledgeBase.channelApi',
  browser_extension: 'knowledgeBase.channelBrowserExtension',
  wechat: 'knowledgeBase.channelWechat',
  wecom: 'knowledgeBase.channelWecom',
  feishu: 'knowledgeBase.channelFeishu',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
};

const getChannelLabel = (channel: string) => {
  const key = channelLabelMap[channel];
  return key ? t(key) : t('knowledgeBase.channelUnknown');
};

const formatSource = (doc: DocumentInfoDocument) => {
  if (doc.type && doc.source) {
    return `${doc.type} · ${doc.source}`;
  }
  return doc.source || doc.type || t('chat.notProvided');
};

const formatFileSize = (size?: number) => {
  if (!size || size <= 0) {
    return t('chat.notProvided');
  }
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = size;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const fixed = value >= 10 || unitIndex === 0 ? 0 : 1;
  return `${value.toFixed(fixed)} ${units[unitIndex]}`;
};

const formatMetadataValue = (value: unknown) => {
  if (value === null || value === undefined) {
    return t('chat.notProvided');
  }
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
};
</script>

<style lang="less" scoped>
@import './tool-results.less';

.document-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.meta-chip {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid @card-border;
  border-radius: 10px;
  padding: 2px 8px;
  line-height: 1.5;
  white-space: nowrap;
}

.documents-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.document-card {
  margin: 0 8px 8px 8px;
  
  .document-header {
    align-items: center;
  }

  .doc-index {
    font-weight: 600;
    color: var(--td-brand-color);
  }

  .doc-title {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .status-pill {
    font-size: 11px;
    color: var(--td-brand-color);
    border: 1px solid rgba(7, 192, 95, 0.3);
    border-radius: 10px;
    padding: 2px 8px;
    line-height: 1.4;
  }
}

.info-section {
  margin-top: 0;
  padding: 6px 0;

  &:first-of-type {
    padding-top: 4px;
  }
}

.info-field {
  display: flex;
  gap: 10px;
  margin-bottom: 5px;
  font-size: 12px;
  line-height: 1.5;

  .field-label {
    color: var(--td-text-color-secondary);
    min-width: 90px;
    font-weight: 500;
  }

  .field-value {
    flex: 1;
    color: var(--td-text-color-primary);
    line-height: 1.5;
  }
}

.metadata-section {
  padding-top: 10px;
  border-top: 1px dashed @card-border;
}

.metadata-list {
  list-style: none;
  margin: 4px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;

  li {
    font-size: 11px;
    color: var(--td-text-color-primary);
    line-height: 1.5;
  }

  .metadata-key {
    font-weight: 600;
    margin-right: 4px;
    color: var(--td-text-color-secondary);
  }

  .metadata-value {
    font-family: var(--app-font-family-mono);
    color: var(--td-text-color-primary);
  }
}

.empty-state {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  text-align: center;
  padding: 14px;
  border: 1px dashed @card-border;
  border-radius: @card-radius;
  background: var(--td-bg-color-secondarycontainer);
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 10px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 4px;
  border-radius: 2px;
  color: var(--td-text-color-primary);
}
</style>
