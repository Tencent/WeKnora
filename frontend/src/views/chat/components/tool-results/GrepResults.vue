<template>
  <div class="grep-results">
    <div v-if="results.length" class="results-list">
      <ResultRow
        v-for="(result, index) in results"
        :key="result.knowledge_id"
        :index="index + 1"
        :title="result.knowledge_title || $t('knowledge.untitledDocument')"
        :meta="formatMeta(result)"
        :popup-key="result.knowledge_id || index"
        :show-popup="!!cleanedSnippet(result)"
        :content="result.match_snippet"
        :knowledge-id="result.knowledge_id"
        :highlight="searchPattern"
        :regex="true"
      />
    </div>

    <div v-else class="empty-state">
      {{ $t('chat.noMatchFound') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { cleanSnippet } from './contentClean';
import ResultRow from './ResultRow.vue';
import type { GrepKnowledgeResult, GrepResultsData } from '@/types/tool-results';

const props = defineProps<{
  data: GrepResultsData;
}>();

const { t } = useI18n();

const results = computed(() => props.data.knowledge_results ?? []);
const searchPattern = computed(() => props.data.query ?? props.data.patterns?.[0] ?? '');

const formatMeta = (result: GrepKnowledgeResult): string => {
  const parts: string[] = [];
  const chunks = result.chunk_hit_count ?? 0;
  if (chunks > 0) {
    parts.push(t('agentStream.grepResults.chunkHits', { count: chunks }));
  }
  const hits = result.total_pattern_hits ?? 0;
  if (hits > 0 && hits !== chunks) {
    parts.push(t('agentStream.grepResults.keywordHits', { count: hits }));
  }
  if (result.title_match) {
    parts.push(t('agentStream.grepResults.titleMatch'));
  }
  return parts.join(' · ');
};

const cleanedSnippet = (result: GrepKnowledgeResult): string =>
  cleanSnippet(result.match_snippet ?? '');
</script>

<style lang="less" scoped>
@import './tool-results.less';

.grep-results {
  display: flex;
  flex-direction: column;
  padding: 0 0 0 12px;
  gap: 3px;
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
  max-height: 200px;
  overflow-y: auto;
}
</style>
