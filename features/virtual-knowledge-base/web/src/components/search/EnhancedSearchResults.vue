<template>
  <t-card title="搜索结果" class="enhanced-search-results">
    <LoadingState v-if="loading" label="执行搜索中" />
    <ErrorState v-else-if="error" :message="error" />
    <t-empty v-else-if="!results.length" description="暂无搜索结果" />
    <t-table v-else :data="results" row-key="document_id" size="small">
      <t-table-column col-key="document_id" title="文档 ID" />
      <t-table-column col-key="score" title="得分">
        <template #cell="{ row }">
          {{ row.score.toFixed(4) }}
        </template>
      </t-table-column>
    </t-table>
  </t-card>
</template>

<script setup lang="ts">
import LoadingState from "@components/common/LoadingState.vue";
import ErrorState from "@components/common/ErrorState.vue";
import type { DocumentScore } from "@api/search";

const props = defineProps<{ results: DocumentScore[]; loading: boolean; error: string | null }>();
</script>

<style scoped>
.enhanced-search-results {
  min-height: 420px;
}
</style>
