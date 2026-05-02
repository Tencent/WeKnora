<template>
  <div class="knowledge-base-list">
    <div v-if="data.knowledge_bases && data.knowledge_bases.length > 0">
      <div class="stats-card">
        <div class="stats-title">{{ $t('chat.knowledgeBaseCount', { count: data.count }) }}</div>
      </div>

      <div class="card-grid">
        <div 
          v-for="kb in data.knowledge_bases" 
          :key="kb.id"
          class="kb-card"
        >
          <div class="kb-header">
            <span class="kb-index">#{{ kb.index }}</span>
            <span class="kb-name">{{ kb.name }}</span>
          </div>
          <div class="kb-body">
            <div class="info-field">
              <span class="field-label">ID:</span>
              <span class="field-value"><code>{{ kb.id }}</code></span>
            </div>
            <div v-if="kb.description" class="kb-description">
              {{ kb.description }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.noKnowledgeBases') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { defineProps } from 'vue';
import type { KnowledgeBaseListData } from '@/types/tool-results';

const props = defineProps<{
  data: KnowledgeBaseListData;
}>();
</script>

<style lang="less" scoped>
@import './tool-results.less';

.knowledge-base-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.kb-card {
  background: @card-bg;
  border: .5px solid @card-border;
  border-radius: @card-radius;
  padding: 12px;
  transition: all 0.2s ease;

  &:hover {
    border-color: @card-hover-border;
    box-shadow: @card-shadow-hover;
  }
}

.kb-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.kb-index {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-weight: 600;
}

.kb-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.kb-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.kb-description {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  margin-top: 4px;
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 4px;
  border-radius: 3px;
}
</style>

