<template>
  <div class="search-results">
    <!-- Search Results List -->
    <div v-if="results && results.length > 0" class="results-list">
      <div 
        v-for="result in results" 
        :key="result.chunk_id"
        class="result-item"
      >
        <t-popup 
          :overlayClassName="`result-popup-${result.chunk_id}`"
          placement="bottom-left"
          width="400"
          :showArrow="false"
          trigger="click"
          destroy-on-close
        >
          <template #content>
            <ContentPopup 
              :content="result.content"
              :chunk-id="result.chunk_id"
              :knowledge-id="result.knowledge_id"
            />
          </template>
          <div class="result-header">
            <div class="result-title">
              <span class="result-index">#{{ result.result_index }}</span>
              <span class="knowledge-title">{{ result.knowledge_title }}</span>
            </div>
          </div>
        </t-popup>
      </div>
    </div>

    <!-- Empty State -->
    <div v-else class="empty-state">
      {{ $t('chat.noSearchResults') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, defineProps, computed } from 'vue';
import type { SearchResultsData, SearchResultItem, RelevanceLevel } from '@/types/tool-results';
import { getMatchTypeIcon } from '@/utils/tool-icons';
import ContentPopup from './ContentPopup.vue';
import { useI18n } from 'vue-i18n';

const props = defineProps<{
  data: SearchResultsData;
  arguments?: Record<string, any> | string;
}>();

const { t } = useI18n();

const results = computed(() => props.data.results || []);
const kbCounts = computed(() => props.data.kb_counts);

// Parse arguments if it's a string
const parsedArguments = computed(() => {
  const args = props.arguments;
  if (!args) return null;
  
  // If it's already an object, return it
  if (typeof args === 'object' && !Array.isArray(args)) {
    return args;
  }
  
  // If it's a string, try to parse it
  if (typeof args === 'string') {
    try {
      return JSON.parse(args);
    } catch (e) {
      console.warn('Failed to parse arguments:', e);
      return null;
    }
  }
  
  return null;
});

// Check if there are search parameters to display (excluding query parameters which are in title)
const hasSearchParams = computed(() => {
  const args = parsedArguments.value;
  if (!args || typeof args !== 'object') return false;
  
  return !!(
    (Array.isArray(args.knowledge_base_ids) && args.knowledge_base_ids.length > 0) ||
    args.top_k || args.vector_threshold || args.keyword_threshold || args.min_score);
});

const hasOtherParams = computed(() => {
  const args = parsedArguments.value;
  if (!args || typeof args !== 'object') return false;
  return !!(args.top_k || args.vector_threshold || args.keyword_threshold || args.min_score);
});


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

.search-results {
  display: flex;
  flex-direction: column;
  padding: 0 0 0 12px;
  gap: 3px;
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.result-item {
  background: transparent;
  border: none;
  border-radius: 0;
  overflow: visible;
}

.result-header {
  padding: 2px 0;
  cursor: pointer;
  user-select: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  transition: color 0.15s ease;
  
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
  line-height: 1.4;
}

.result-index {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-weight: 600;
  flex-shrink: 0;
}

.relevance-badge {
  flex-shrink: 0;
  font-size: 10px;
  padding: 2px 5px;
  border-radius: 3px;
}

.knowledge-title {
  font-size: 12px;
  color: var(--td-text-color-primary);
  flex: 1;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

// Popup overlay styles
:deep([class*="result-popup-"]) {
  .t-popup__content {
    max-height: 400px;
    max-width: 500px;
    overflow-y: auto;
    overflow-x: hidden;
    padding: 0;
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
    word-wrap: break-word;
    word-break: break-word;
  }
}

.info-section {
  margin-top: 6px;
  
  &:first-child {
    margin-top: 0;
  }
}

.full-content {
  font-size: 12px;
  color: var(--td-text-color-primary);
  line-height: 1.6;
  padding: 10px;
  background: var(--td-bg-color-container);
  border-radius: 4px;
  border: 1px solid var(--td-component-stroke);
  white-space: pre-wrap;
  word-break: break-word;
  margin-bottom: 6px;
}

.info-field {
  font-size: 11px;
  margin-bottom: 4px;
  
  .field-label {
    min-width: 60px;
    font-size: 10px;
  }
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 10px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 4px;
  border-radius: 2px;
}
</style>


