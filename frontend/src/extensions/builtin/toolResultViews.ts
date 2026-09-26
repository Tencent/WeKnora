// The chat renderings of WeKnora's own tools, keyed by the display_type the
// backend puts on each tool result.
import type { Component } from 'vue'

import ChunkDetail from '@/views/chat/components/tool-results/ChunkDetail.vue'
import DatabaseQuery from '@/views/chat/components/tool-results/DatabaseQuery.vue'
import DocumentInfo from '@/views/chat/components/tool-results/DocumentInfo.vue'
import GraphQueryResults from '@/views/chat/components/tool-results/GraphQueryResults.vue'
import GrepResults from '@/views/chat/components/tool-results/GrepResults.vue'
import KnowledgeBaseList from '@/views/chat/components/tool-results/KnowledgeBaseList.vue'
import KnowledgeChunksList from '@/views/chat/components/tool-results/KnowledgeChunksList.vue'
import McpToolResult from '@/views/chat/components/tool-results/McpToolResult.vue'
import PlanDisplay from '@/views/chat/components/tool-results/PlanDisplay.vue'
import PluginToolView from '@/views/chat/components/tool-results/PluginToolView.vue'
import ReadSkillResult from '@/views/chat/components/tool-results/ReadSkillResult.vue'
import RelatedChunks from '@/views/chat/components/tool-results/RelatedChunks.vue'
import SandboxFilesResult from '@/views/chat/components/tool-results/SandboxFilesResult.vue'
import SearchResults from '@/views/chat/components/tool-results/SearchResults.vue'
import ShellExecResult from '@/views/chat/components/tool-results/ShellExecResult.vue'
import ThinkingDisplay from '@/views/chat/components/tool-results/ThinkingDisplay.vue'
import WebFetchResults from '@/views/chat/components/tool-results/WebFetchResults.vue'
import WebSearchResults from '@/views/chat/components/tool-results/WebSearchResults.vue'
import WikiEditResult from '@/views/chat/components/tool-results/WikiEditResult.vue'
import WriteSandboxFileResult from '@/views/chat/components/tool-results/WriteSandboxFileResult.vue'

import { CORE_PLUGIN_ID } from '../registry'
import { toolResultViews, type ToolResultView } from '../toolResultViews'

type View = Omit<ToolResultView, 'key' | 'pluginId'>

const dataOnly = (component: Component): View => ({ component })

const VIEWS: Record<string, View> = {
  search_results: { component: SearchResults, props: (c) => ({ data: c.data, arguments: c.arguments }) },
  chunk_detail: dataOnly(ChunkDetail),
  related_chunks: dataOnly(RelatedChunks),
  knowledge_base_list: dataOnly(KnowledgeBaseList),
  document_info: dataOnly(DocumentInfo),
  graph_query_results: dataOnly(GraphQueryResults),
  thinking: dataOnly(ThinkingDisplay),
  plan: dataOnly(PlanDisplay),
  database_query: dataOnly(DatabaseQuery),
  web_search_results: dataOnly(WebSearchResults),
  web_fetch_results: dataOnly(WebFetchResults),
  grep_results: dataOnly(GrepResults),
  knowledge_chunks_list: dataOnly(KnowledgeChunksList),
  wiki_write_page: dataOnly(WikiEditResult),
  wiki_replace_text: dataOnly(WikiEditResult),
  wiki_rename_page: dataOnly(WikiEditResult),
  wiki_delete_page: dataOnly(WikiEditResult),
  shell_exec: {
    component: ShellExecResult,
    props: (c) => ({ data: c.data, output: c.output, arguments: c.arguments }),
  },
  list_sandbox_files: dataOnly(SandboxFilesResult),
  write_sandbox_file: dataOnly(WriteSandboxFileResult),
  edit_sandbox_file: dataOnly(WriteSandboxFileResult),
  read_skill: dataOnly(ReadSkillResult),
  mcp_discovery: {
    component: McpToolResult,
    props: (c) => ({ discovery: true, data: c.data, output: c.output, arguments: c.arguments, success: c.success }),
  },
  // Results of plugin tools, shown with the view the plugin declared.
  plugin_tool_view: {
    component: PluginToolView,
    props: (c) => ({ data: c.data, output: c.output, arguments: c.arguments, success: c.success }),
  },
  mcp_call: {
    component: McpToolResult,
    props: (c) => ({ discovery: false, data: c.data, output: c.output, arguments: c.arguments, success: c.success }),
  },
}

let registered = false

/** Registers the builtin tool result views once. */
export function registerBuiltinToolResultViews() {
  if (registered) return
  registered = true
  for (const [key, view] of Object.entries(VIEWS)) {
    toolResultViews.register({ key, pluginId: CORE_PLUGIN_ID, ...view })
  }
}
