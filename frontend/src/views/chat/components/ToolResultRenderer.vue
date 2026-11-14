<template>
  <div class="tool-result-renderer">
    <!-- Search Results -->
    <SearchResults v-if="displayType === 'search_results'" :data="toolData as SearchResultsData"
      :arguments="toolArguments" />

    <!-- Chunk Detail -->
    <ChunkDetail v-else-if="displayType === 'chunk_detail'" :data="toolData as ChunkDetailData" />

    <!-- Related Chunks -->
    <RelatedChunks v-else-if="displayType === 'related_chunks'" :data="toolData as RelatedChunksData" />

    <!-- Knowledge Base List -->
    <KnowledgeBaseList v-else-if="displayType === 'knowledge_base_list'" :data="toolData as KnowledgeBaseListData" />

    <!-- Document Info -->
    <DocumentInfo v-else-if="displayType === 'document_info'" :data="toolData as DocumentInfoData" />

    <!-- Graph Query Results -->
    <GraphQueryResults v-else-if="displayType === 'graph_query_results'" :data="toolData as GraphQueryResultsData" />

    <!-- Thinking Display -->
    <ThinkingDisplay v-else-if="displayType === 'thinking'" :data="toolData as ThinkingData" />

    <!-- Plan Display -->
    <PlanDisplay v-else-if="displayType === 'plan'" :data="toolData as PlanData" />

    <!-- Database Query Display -->
    <DatabaseQuery v-else-if="displayType === 'database_query'" :data="toolData as DatabaseQueryData" />

    <!-- Web Search Results Display -->
    <WebSearchResults v-else-if="displayType === 'web_search_results'" :data="toolData as WebSearchResultsData" />

    <!-- Web Fetch Results Display -->
    <WebFetchResults v-else-if="displayType === 'web_fetch_results'" :data="toolData as WebFetchResultsData" />

    <!-- Grep Results Display -->
    <GrepResults v-else-if="displayType === 'grep_results'" :data="toolData as GrepResultsData" />

    <!-- Knowledge Chunks List -->
    <KnowledgeChunksList v-else-if="displayType === 'knowledge_chunks_list'"
      :data="toolData as KnowledgeChunksListData" />

    <!-- Wiki Edit Results Display -->
    <WikiEditResult
      v-else-if="displayType === 'wiki_write_page' || displayType === 'wiki_replace_text' || displayType === 'wiki_rename_page' || displayType === 'wiki_delete_page'"
      :data="toolData as WikiEditData" />

    <ShellExecResult
      v-else-if="displayType === 'shell_exec'"
      :data="shellExecData"
      :output="shellExecOutput"
      :arguments="toolArguments"
    />

    <div v-else-if="displayType === 'work_wait'" class="work-wait-results">
      <p v-if="workNote" class="work-wait-note">{{ workNote }}</p>
      <ShellExecResult
        v-for="(item, index) in workItems"
        :key="item.work_id || index"
        :data="shellItemResult(item)"
      />
      <div v-if="!workItems.length" class="fallback-output">
        <div class="fallback-header">
          <span class="fallback-label">{{ $t('chat.rawOutputLabel') }}</span>
        </div>
        <div class="detail-output-wrapper">
          <div class="detail-output">{{ output }}</div>
        </div>
      </div>
    </div>

    <SandboxFilesResult
      v-else-if="displayType === 'list_sandbox_files'"
      :data="toolData as ListSandboxFilesData"
    />

    <WriteSandboxFileResult
      v-else-if="displayType === 'write_sandbox_file' || displayType === 'edit_sandbox_file'"
      :data="toolData as WriteSandboxFileData"
    />

    <ReadSkillResult
      v-else-if="displayType === 'read_skill'"
      :data="toolData as ReadSkillData"
    />

    <ImageViewResult
      v-else-if="displayType === 'image_view'"
      :data="toolData as ImageViewData"
    />

    <McpToolResult
      v-else-if="displayType === 'mcp_discovery' || displayType === 'mcp_call'"
      :discovery="displayType === 'mcp_discovery'" :data="toolData"
      :output="output" :arguments="toolArguments" :success="success"
    />

    <OperationsResult
      v-else-if="displayType === 'find_operations' || displayType === 'run_operation'"
      :mode="displayType === 'run_operation' ? 'run' : 'find'"
      :output="output" :data="toolData" :success="success"
    />

    <WeKnoraDocResult
      v-else-if="displayType === 'search_weknora_docs' || displayType === 'read_weknora_doc'"
      :mode="displayType === 'read_weknora_doc' ? 'read' : 'search'"
      :output="output" :data="toolData" :success="success"
    />

    <!-- Unmatched display types still reuse the shell card when the payload is a work list. -->
    <div v-else-if="workItems.length" class="work-wait-results">
      <p v-if="workNote" class="work-wait-note">{{ workNote }}</p>
      <ShellExecResult
        v-for="(item, index) in workItems"
        :key="item.work_id || index"
        :data="shellItemResult(item)"
      />
    </div>

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
import { computed } from 'vue';
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
  KnowledgeChunksListData,
  WikiEditData,
  ListSandboxFilesData,
  WriteSandboxFileData,
  ReadSkillData,
  ImageViewData
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
import KnowledgeChunksList from './tool-results/KnowledgeChunksList.vue';
import WikiEditResult from './tool-results/WikiEditResult.vue';
import ShellExecResult from './tool-results/ShellExecResult.vue';
import SandboxFilesResult from './tool-results/SandboxFilesResult.vue';
import WriteSandboxFileResult from './tool-results/WriteSandboxFileResult.vue';
import ReadSkillResult from './tool-results/ReadSkillResult.vue';
import McpToolResult from './tool-results/McpToolResult.vue';
import ImageViewResult from './tool-results/ImageViewResult.vue';
import OperationsResult from './tool-results/OperationsResult.vue';
import WeKnoraDocResult from './tool-results/WeKnoraDocResult.vue';

interface Props {
  success?: boolean;
  displayType?: DisplayType;
  toolData?: Record<string, any>;
  output?: string;
  arguments?: Record<string, any>;
}

const props = withDefaults(defineProps<Props>(), { success: undefined });

const displayType = computed(() => props.displayType);
const toolData = computed(() => props.toolData || {});
const output = computed(() => props.output || '');
const toolArguments = computed(() => props.arguments || {});

const shellExecData = computed(() => {
  const nested = toolData.value.shell_job
  if (nested && typeof nested === 'object' && !Array.isArray(nested)) return nested
  return toolData.value
})
const shellExecOutput = computed(() => (
  shellExecData.value === toolData.value ? output.value : ''
))

const workItems = computed(() => {
  const data = toolData.value
  if (Array.isArray(data.items)) return data.items
  const nested = data.work
  if (nested && typeof nested === 'object' && Array.isArray(nested.items)) return nested.items
  return []
})
const workNote = computed(() => {
  const data = toolData.value
  if (typeof data.note === 'string' && data.note) return data.note
  const nested = data.work
  if (nested && typeof nested === 'object' && typeof nested.note === 'string') return nested.note
  return ''
})

function shellItemResult(item: Record<string, any>) {
  const result = item?.result
  if (result && typeof result === 'object') return result
  return {}
}
</script>

<style lang="less" scoped>
.tool-result-renderer {
  margin: 0;
}

.work-wait-results {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.work-wait-note {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
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
      font-size: var(--app-text-sm);
      color: var(--td-text-color-secondary);
      font-weight: 500;
      line-height: 1.5;
    }
  }

  .detail-output-wrapper {
    position: relative;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-sm);
    overflow: hidden;
    margin: 0;
    padding: 0;

    .detail-output {
      font-family: var(--app-font-family-mono);
      font-size: var(--app-text-sm);
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
        border-radius: var(--app-radius-xs);
      }

      &::-webkit-scrollbar-thumb {
        background: var(--td-component-border);
        border-radius: var(--app-radius-xs);

        &:hover {
          background: var(--td-text-color-placeholder);
        }
      }
    }
  }
}
</style>
