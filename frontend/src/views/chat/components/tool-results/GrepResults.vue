<template>
  <div class="grep-results">
    <div v-if="results.length" class="results-list">
      <div
        v-for="(result, index) in results"
        :key="result.knowledge_id"
        class="result-row"
      >
        <div class="result-row__index">#{{ index + 1 }}</div>
        <div class="result-row__title">{{ result.knowledge_title || $t('knowledge.untitledDocument') }}</div>
      </div>
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.noMatchFound') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { GrepResultsData } from '@/types/tool-results';

const { t } = useI18n();

const props = defineProps<{
  data: GrepResultsData;
}>();

const patterns = computed(() => props.data.patterns ?? []);
const results = computed(() => props.data.knowledge_results ?? []);

const resultCount = computed(() => props.data.result_count ?? results.value.length);
const totalMatches = computed(() => props.data.total_matches ?? results.value.length);
const maxResults = computed(() => props.data.max_results ?? results.value.length);
const hasMoreResults = computed(() => totalMatches.value > resultCount.value);

// Compact view, no per-pattern stats
</script>

<style lang="less" scoped>
@import './tool-results.less';

.grep-results {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.summary-inline {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  display: flex;
  align-items: center;
  gap: 6px;

  &__divider {
    color: var(--td-text-color-disabled);
  }

  &__truncated {
    color: var(--td-warning-color);
  }
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px 10px 12px;
}

.result-row {
  display: grid;
  grid-template-columns: 40px minmax(120px, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 4px 10px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  font-size: 12px;
  line-height: 1.4;
}

.result-row__index {
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}

.result-row__title {
  color: var(--td-text-color-primary);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.result-row__meta {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.meta-pill {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 999px;
  padding: 2px 8px;
}

.empty-state {
  padding: 20px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-style: italic;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border: 1px dashed var(--td-component-stroke);
}
</style>
