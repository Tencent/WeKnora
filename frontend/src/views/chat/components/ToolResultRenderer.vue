<template>
  <div class="tool-result-renderer">
    <!-- Search Results -->
    <SearchResults 
      v-if="displayType === 'search_results'" 
      :data="toolData as SearchResultsData" 
      :arguments="toolArguments"
    />
    
    <!-- Chunk Detail -->
    <ChunkDetail 
      v-else-if="displayType === 'chunk_detail'" 
      :data="toolData as ChunkDetailData" 
    />
    
    <!-- Related Chunks -->
    <RelatedChunks 
      v-else-if="displayType === 'related_chunks'" 
      :data="toolData as RelatedChunksData" 
    />
    
    <!-- Knowledge Base List -->
    <KnowledgeBaseList 
      v-else-if="displayType === 'knowledge_base_list'" 
      :data="toolData as KnowledgeBaseListData" 
    />
    
    <!-- Document Info -->
    <DocumentInfo 
      v-else-if="displayType === 'document_info'" 
      :data="toolData as DocumentInfoData" 
    />
    
    <!-- Graph Query Results -->
    <GraphQueryResults 
      v-else-if="displayType === 'graph_query_results'" 
      :data="toolData as GraphQueryResultsData" 
    />
    
    <!-- Thinking Display -->
    <ThinkingDisplay 
      v-else-if="displayType === 'thinking'" 
      :data="toolData as ThinkingData" 
    />
    
    <!-- Plan Display -->
    <PlanDisplay 
      v-else-if="displayType === 'plan'" 
      :data="toolData as PlanData" 
    />
    
    <!-- Database Query Display -->
    <DatabaseQuery 
      v-else-if="displayType === 'database_query'" 
      :data="toolData as DatabaseQueryData" 
    />
    
    <!-- Web Search Results Display -->
    <WebSearchResults 
      v-else-if="displayType === 'web_search_results'" 
      :data="toolData as WebSearchResultsData" 
    />
    
    <!-- Web Fetch Results Display -->
    <WebFetchResults
      v-else-if="displayType === 'web_fetch_results'"
      :data="toolData as WebFetchResultsData"
    />
    
    <!-- Grep Results Display -->
    <GrepResults
      v-else-if="displayType === 'grep_results'"
      :data="toolData as GrepResultsData"
    />
    
    <!-- Wiki Edit Results Display -->
    <WikiEditResult
      v-else-if="displayType === 'wiki_write_page' || displayType === 'wiki_replace_text' || displayType === 'wiki_rename_page' || displayType === 'wiki_delete_page'"
      :data="toolData as WikiEditData"
    />
    
    <!-- Fallback: Display raw output -->
    <div v-else class="fallback-output">
      <div class="fallback-header">
        <span class="fallback-label">{{ $t('chat.rawOutputLabel') }}</span>
      </div>
      <div class="detail-output-wrapper">
        <div class="detail-output">{{ output }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { defineProps, computed } from 'vue';
import type { 
  DisplayType,
  SearchResultsData,
  ChunkDetailData,
  RelatedChunksData,
  KnowledgeBaseListData,
  DocumentInfoData,
  GraphQueryResultsData,
  ThinkingData,
  PlanData,
  DatabaseQueryData,
  WebSearchResultsData,
  WebFetchResultsData,
  GrepResultsData,
  WikiEditData
} from '@/types/tool-results';

import SearchResults from './tool-results/SearchResults.vue';
import ChunkDetail from './tool-results/ChunkDetail.vue';
import RelatedChunks from './tool-results/RelatedChunks.vue';
import KnowledgeBaseList from './tool-results/KnowledgeBaseList.vue';
import DocumentInfo from './tool-results/DocumentInfo.vue';
import GraphQueryResults from './tool-results/GraphQueryResults.vue';
import ThinkingDisplay from './tool-results/ThinkingDisplay.vue';
import PlanDisplay from './tool-results/PlanDisplay.vue';
import DatabaseQuery from './tool-results/DatabaseQuery.vue';
import WebSearchResults from './tool-results/WebSearchResults.vue';
import WebFetchResults from './tool-results/WebFetchResults.vue';
import GrepResults from './tool-results/GrepResults.vue';
import WikiEditResult from './tool-results/WikiEditResult.vue';

interface Props {
  displayType?: DisplayType;
  toolData?: Record<string, any>;
  output?: string;
  arguments?: Record<string, any>;
}

const props = defineProps<Props>();

const displayType = computed(() => props.displayType);
const toolData = computed(() => props.toolData || {});
const output = computed(() => props.output || '');
const toolArguments = computed(() => props.arguments || {});
</script>

<style lang="less" scoped>
.tool-result-renderer {
  margin: 0;
}

.fallback-output {
  margin: 12px 0;
  padding: 0;
  
  .fallback-header {
    display: flex;
    align-items: center;
    margin-bottom: 10px;
    padding: 0 4px;
    
    .fallback-label {
      font-size: 12px;
      color: var(--td-text-color-secondary);
      font-weight: 500;
      line-height: 1.5;
    }
  }
  
  .detail-output-wrapper {
    position: relative;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    overflow: hidden;
    margin: 0;
    padding: 0;
    
    .detail-output {
      font-family: var(--app-font-family-mono);
      font-size: 12px;
      color: var(--td-text-color-primary);
      padding: 16px;
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.6;
      max-height: 400px;
      overflow-y: auto;
      overflow-x: auto;
      background: var(--td-bg-color-container);
      display: block;
      
      // 滚动条样式
      &::-webkit-scrollbar {
        width: 8px;
        height: 8px;
      }
      
      &::-webkit-scrollbar-track {
        background: var(--td-bg-color-secondarycontainer);
        border-radius: 4px;
      }
      
      &::-webkit-scrollbar-thumb {
        background: var(--td-component-border);
        border-radius: 4px;
        
        &:hover {
          background: var(--td-text-color-placeholder);
        }
      }
    }
  }
}
</style>

