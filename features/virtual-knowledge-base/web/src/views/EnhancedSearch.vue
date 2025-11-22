<template>
  <div class="enhanced-search">
    <t-row :gutter="24">
      <t-col :xs="12" :md="5" :lg="4">
        <EnhancedSearchPanel :virtual-k-bs="virtualKBs" @search="handleSearch" />
      </t-col>
      <t-col :xs="12" :md="7" :lg="8">
        <EnhancedSearchResults :results="results" :loading="loading" :error="error" />
      </t-col>
    </t-row>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import EnhancedSearchPanel from "@components/search/EnhancedSearchPanel.vue";
import EnhancedSearchResults from "@components/search/EnhancedSearchResults.vue";
import { enhancedSearch, type DocumentScore, type EnhancedSearchRequest } from "@api/search";
import { fetchVirtualKBs, type VirtualKB } from "@api/virtualKB";

const virtualKBs = ref<VirtualKB[]>([]);
const results = ref<DocumentScore[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const loadVirtualKBs = async () => {
  virtualKBs.value = await fetchVirtualKBs();
};

const handleSearch = async (payload: EnhancedSearchRequest) => {
  try {
    loading.value = true;
    const data = await enhancedSearch(payload);
    results.value = data.results;
    error.value = null;
  } catch (err) {
    error.value = (err as Error).message ?? "搜索失败";
  } finally {
    loading.value = false;
  }
};

loadVirtualKBs();
</script>

<style scoped>
.enhanced-search {
  padding: 24px;
}
</style>
