<template>
  <div class="graph-query-results">
    <!-- Graph Configuration Card -->
    <div v-if="data.graph_config" class="stats-card">
      <div class="stats-title">{{ $t('chat.graphConfigTitle') }}</div>
      <div class="info-field">
        <span class="field-label">{{ $t('chat.entityTypesLabel') }}</span>
        <span class="field-value">{{ data.graph_config.nodes.join(', ') }}</span>
      </div>
      <div class="info-field">
        <span class="field-label">{{ $t('chat.relationTypesLabel') }}</span>
        <span class="field-value">{{ data.graph_config.relations.join(', ') }}</span>
      </div>
    </div>

    <!-- Results List -->
    <div v-if="data.results && data.results.length > 0" class="results-list">
      <div class="results-header">
        {{ $t('chat.graphResultsHeader', { count: data.count }) }}
      </div>
      
      <div 
        v-for="result in data.results" 
        :key="result.chunk_id"
        class="result-card"
      >
        <div class="result-header" @click="toggleResult(result.chunk_id)">
          <div class="result-title">
            <span class="result-index">#{{ result.result_index }}</span>
            <span class="relevance-badge" :class="getRelevanceClass(result.relevance_level)">
              {{ getRelevanceLabel(result.relevance_level) }}
            </span>
            <span class="knowledge-title">{{ result.knowledge_title }}</span>
          </div>
          <div class="result-meta">
            <span class="score">{{ (result.score * 100).toFixed(0) }}%</span>
            <span class="expand-icon" :class="{ expanded: expandedResults.includes(result.chunk_id) }">
              ▶
            </span>
          </div>
        </div>
        
        <div class="result-content" :class="{ expanded: expandedResults.includes(result.chunk_id) }">
          <div class="full-content">{{ result.content }}</div>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.graphNoResults') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, defineProps } from 'vue';
import type { GraphQueryResultsData, RelevanceLevel } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

const props = defineProps<{
  data: GraphQueryResultsData;
}>();

const { t } = useI18n();

const expandedResults = ref<string[]>([]);

const toggleResult = (chunkId: string) => {
  const index = expandedResults.value.indexOf(chunkId);
  if (index > -1) {
    expandedResults.value.splice(index, 1);
  } else {
    expandedResults.value.push(chunkId);
  }
};

const getRelevanceClass = (level: RelevanceLevel): string => {
  const classMap: Record<RelevanceLevel, string> = {
    'High Relevance': 'high',
    'Medium Relevance': 'medium',
    'Low Relevance': 'low',
    'Weak Relevance': 'weak',
  };
  return classMap[level] || 'weak';
};

const getRelevanceLabel = (level: RelevanceLevel): string => {
  const labelMap: Record<RelevanceLevel, string> = {
    'High Relevance': t('chat.relevanceHigh'),
    'Medium Relevance': t('chat.relevanceMedium'),
    'Low Relevance': t('chat.relevanceLow'),
    'Weak Relevance': t('chat.relevanceWeak'),
  };
  return labelMap[level] || level;
};
</script>

<style lang="less" scoped>
@import './tool-results.less';

.graph-query-results {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.results-header {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  padding: 4px 0;
}

.result-index {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  font-weight: 600;
}

.knowledge-title {
  font-size: 13px;
  color: var(--td-text-color-primary);
  flex: 1;
}

.score {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-weight: 500;
}
</style>

