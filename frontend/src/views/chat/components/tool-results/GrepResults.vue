<template>
  <div class="grep-results">
    <div v-if="rows.length" class="results-list">
      <ResultRow v-for="(result, index) in rows" :key="result.key" :index="index + 1" :title="result.title"
        :meta="result.meta" :popup-key="result.key" :show-popup="result.chunks.length > 0 || !!result.snippet"
        :content="result.chunks.length === 1 ? result.snippet : undefined"
        :chunks="result.chunks.length > 1 ? result.chunks : undefined" :chunk-id="result.chunkId"
        :knowledge-id="result.knowledgeId" :highlight="searchPattern" :regex="true" />
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
import { groupGrepChunkResults } from '@/utils/grepResultsGroup';
import type { GrepKnowledgeResult, GrepResultsData } from '@/types/tool-results';

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
  chunks: { content: string; chunk_id: string; knowledge_id: string }[];
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

const rowFromGroupedChunk = (group: ReturnType<typeof groupGrepChunkResults>[number]): GrepRow => {
  const title = group.title || t('knowledge.untitledDocument');
  const meta = group.is_faq
    ? t('agentStream.grepResults.faqEntry')
    : formatKnowledgeMeta({
      knowledge_id: group.knowledge_id,
      knowledge_base_id: '',
      knowledge_title: title,
      chunk_hit_count: group.chunk_hit_count,
      total_pattern_hits: group.chunk_hit_count,
      distinct_patterns: 1,
      pattern_counts: {},
      title_match: group.title_match,
    });
  return {
    key: group.key,
    title,
    meta,
    snippet: cleanSnippet(group.match_snippet),
    chunks: group.chunks.map((chunk) => ({
      content: cleanSnippet(chunk.content),
      chunk_id: chunk.chunk_id,
      knowledge_id: chunk.knowledge_id,
    })),
    chunkId: group.chunks.length === 1 ? group.chunks[0].chunk_id : undefined,
    knowledgeId: group.knowledge_id,
  };
};

const rowFromKnowledge = (result: GrepKnowledgeResult): GrepRow => ({
  key: result.knowledge_id,
  title: result.faq_question || result.knowledge_title || t('knowledge.untitledDocument'),
  meta: formatKnowledgeMeta(result),
  snippet: cleanSnippet(result.match_snippet ?? ''),
  chunks: result.match_snippet
    ? [{
      content: cleanSnippet(result.match_snippet ?? ''),
      chunk_id: '',
      knowledge_id: result.knowledge_id,
    }]
    : [],
  knowledgeId: result.knowledge_id,
});

const rows = computed((): GrepRow[] => {
  const chunkRows = props.data.chunk_results;
  if (chunkRows?.length) {
    return groupGrepChunkResults(chunkRows).map(rowFromGroupedChunk);
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
