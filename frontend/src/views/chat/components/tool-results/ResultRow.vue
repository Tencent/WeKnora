<template>
  <div class="result-row" :class="{ 'result-row--has-preview': showPopup }">
    <span class="result-row__index">#{{ index }}</span>
    <span class="result-row__title" :title="title">{{ title }}</span>
    <span v-if="meta" class="result-row__meta">{{ meta }}</span>

    <t-popup
      v-if="showPopup"
      :overlayClassName="`tool-result-popup tool-result-popup-${popupKey}`"
      placement="bottom-left"
      :width="400"
      :showArrow="false"
      trigger="hover"
      destroy-on-close
    >
      <template #content>
        <ContentPopup
          :content="content"
          :chunks="chunks"
          :chunk-id="chunkId"
          :knowledge-id="knowledgeId"
          :highlight="highlight"
          :regex="regex"
        />
      </template>
      <span class="result-row__preview" :aria-label="$t('chat.previewContent')">
        <browse-icon />
      </span>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { BrowseIcon } from 'tdesign-icons-vue-next';
import ContentPopup from './ContentPopup.vue';

interface ChunkContent {
  content: string;
  chunk_id?: string;
  knowledge_id?: string;
}

withDefaults(
  defineProps<{
    index: number;
    title: string;
    meta?: string;
    popupKey: string | number;
    showPopup?: boolean;
    content?: string;
    chunks?: ChunkContent[];
    chunkId?: string;
    knowledgeId?: string;
    highlight?: string;
    regex?: boolean;
  }>(),
  { showPopup: true },
);
</script>

<style lang="less" scoped>
.result-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  padding: 4px 8px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.4;
  user-select: none;

  &--has-preview:hover {
    background: var(--td-bg-color-secondarycontainer);

    .result-row__preview {
      opacity: 1;
    }
  }
}

.result-row__preview {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 4px;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  opacity: 0.5;
  transition: opacity 0.15s ease, color 0.15s ease, background 0.15s ease;

  &:hover {
    opacity: 1;
    color: var(--td-brand-color);
    background: var(--td-bg-color-container);
  }
}

.result-row__index {
  flex-shrink: 0;
  min-width: 22px;
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}

.result-row__title {
  flex: 1;
  min-width: 0;
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.result-row__meta {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}
</style>

<!-- Non-scoped: the popup overlay is teleported to <body>, so style it globally. -->
<style lang="less">
.tool-result-popup .t-popup__content {
  padding: 0;
  background: transparent;
  border: none;
  box-shadow: none;
}
</style>
