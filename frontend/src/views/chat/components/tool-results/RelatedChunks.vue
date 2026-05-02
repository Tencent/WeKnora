<template>
  <div class="related-chunks">
    <div v-if="data.chunks && data.chunks.length > 0" class="chunks-list">
      <div 
        v-for="chunk in data.chunks" 
        :key="chunk.chunk_id"
        class="result-item"
      >
        <t-popup 
          :overlayClassName="`chunk-popup-${chunk.chunk_id}`"
          placement="bottom-left"
          width="400"
          :showArrow="false"
          trigger="click"
          destroy-on-close
        >
          <template #content>
            <ContentPopup 
              :content="chunk.content"
              :chunk-id="chunk.chunk_id"
            />
          </template>
          <div class="result-header">
            <div class="result-title">
              <span class="chunk-index">{{ $t('chat.chunkIndexLabel', { index: chunk.index }) }}</span>
              <span class="chunk-position">{{ $t('chat.chunkPositionLabel', { position: chunk.chunk_index }) }}</span>
            </div>
          </div>
        </t-popup>
      </div>
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.noRelatedChunks') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, defineProps, computed } from 'vue';
import type { RelatedChunksData } from '@/types/tool-results';
import ContentPopup from './ContentPopup.vue';

const props = defineProps<{
  data: RelatedChunksData;
}>();

</script>

<style lang="less" scoped>
@import './tool-results.less';

.related-chunks {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 0 0 0 12px;
  
  .info-section {
    margin-bottom: 8px;
    padding: 0;
    
    .info-field {
      font-size: 11px;
      margin-bottom: 4px;
      
      .field-label {
        font-size: 11px;
        color: var(--td-text-color-placeholder);
        min-width: 70px;
      }
      
      .field-value {
        font-size: 11px;
        color: var(--td-text-color-secondary);
      }
    }
  }
}

.chunks-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.result-item {
  background: transparent;
  border: none;
  border-radius: 0;
  overflow: visible;
}

.result-header {
  padding: 4px 0;
  cursor: pointer;
  user-select: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  transition: color 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  
  &:hover {
    color: var(--td-brand-color);
  }
}

.result-title {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  font-size: 12px;
}

.chunk-index {
  font-size: 12px;
  color: var(--td-text-color-primary);
  font-weight: 600;
  flex-shrink: 0;
}

.chunk-position {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}


// Popup overlay styles
:deep([class*="chunk-popup-"]) {
  .t-popup__content {
    max-height: 400px;
    max-width: 500px;
    overflow-y: auto;
    overflow-x: hidden;
    padding: 0;
    border-radius: 6px;
    box-shadow: var(--td-shadow-3);
    word-wrap: break-word;
    word-break: break-word;
  }
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 4px;
  border-radius: 3px;
}
</style>

