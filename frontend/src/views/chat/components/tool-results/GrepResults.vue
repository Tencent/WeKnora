<template>
  <div class="grep-results">
    <div v-if="rows.length" class="results-list">
      <ResultRow
        v-for="(result, index) in rows"
        :key="result.key"
        :index="index + 1"
        :title="result.title"
        :meta="result.meta"
        :popup-key="result.key"
        :show-popup="!!result.snippet"
        :content="result.snippet"
        :chunk-id="result.chunkId"
        :knowledge-id="result.knowledgeId"
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
import type { GrepChunkResult, GrepKnowledgeResult, GrepResultsData } from '@/types/tool-results';

const props = defineProps<{
  data: GrepResultsData;
}>();

const { t } = useI18n();

const searchPattern = computed(() => props.data.query ?? props.data.patterns?.[0] ?? '');

type GrepRow = {
  key: string;
  title: string;
  meta: string;
  snippet: string;
  chunkId?: string;
  knowledgeId?: string;
};

const formatKnowledgeMeta = (result: GrepKnowledgeResult): string => {
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

const rowFromChunk = (result: GrepChunkResult): GrepRow => {
  const isFAQ = !!result.faq_id || result.chunk_type === 'faq';
  const title = result.faq_question || result.knowledge_title || t('knowledge.untitledDocument');
  const meta = isFAQ
    ? t('agentStream.grepResults.faqEntry')
    : formatKnowledgeMeta({
        knowledge_id: result.knowledge_id,
        knowledge_base_id: result.knowledge_base_id,
        knowledge_title: result.knowledge_title,
        chunk_hit_count: 1,
        total_pattern_hits: 1,
        distinct_patterns: 1,
        pattern_counts: {},
        title_match: !!result.title_match,
      });
  return {
    key: result.faq_id || result.chunk_id || String(result.index ?? '') || result.knowledge_id,
    title,
    meta,
    snippet: cleanSnippet(result.match_snippet ?? ''),
    chunkId: result.faq_id || result.chunk_id,
    knowledgeId: result.knowledge_id,
  };
};

const rowFromKnowledge = (result: GrepKnowledgeResult): GrepRow => ({
  key: result.knowledge_id,
  title: result.faq_question || result.knowledge_title || t('knowledge.untitledDocument'),
  meta: formatKnowledgeMeta(result),
  snippet: cleanSnippet(result.match_snippet ?? ''),
  knowledgeId: result.knowledge_id,
});

const rows = computed((): GrepRow[] => {
  const chunkRows = props.data.chunk_results;
  if (chunkRows?.length) {
    return chunkRows.map(rowFromChunk);
  }
  return (props.data.knowledge_results ?? []).map(rowFromKnowledge);
});
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
