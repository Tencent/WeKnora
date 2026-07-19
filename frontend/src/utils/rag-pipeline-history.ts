export const RAG_PIPELINE_TOOL_NAMES = new Set(['query_understand', 'knowledge_search'])

/** Tools rendered on the quick-answer timeline (includes pre-RAG attachment prep). */
export const RAG_TIMELINE_TOOL_NAMES = new Set([
  ...RAG_PIPELINE_TOOL_NAMES,
  'attachment_parsing',
  'image_analysis',
])

type RagHistoryReference = {
  chunk_type?: string
  knowledge_id?: string
  knowledge_title?: string
}

function inferRetrievalSearchSource(refs: RagHistoryReference[]): 'knowledge' | 'web' | 'mixed' {
  let docCount = 0
  let webCount = 0
  for (const ref of refs) {
    if (ref.chunk_type === 'web_search') {
      webCount++
    } else {
      docCount++
    }
  }
  if (docCount > 0 && webCount > 0) return 'mixed'
  if (webCount > 0) return 'web'
  return 'knowledge'
}

type RagHistoryMessage = {
  knowledge_references?: RagHistoryReference[]
  agentEventStream?: Array<Record<string, unknown>>
}

export function hasRagPipelineToolEvents(stream: Array<Record<string, unknown>> | undefined): boolean {
  if (!stream?.length) return false
  return stream.some((event) => {
    return (
      event.type === 'tool_call' &&
      typeof event.tool_name === 'string' &&
      RAG_PIPELINE_TOOL_NAMES.has(event.tool_name)
    )
  })
}

export function synthesizeRagPipelineToolEvents(
  item: RagHistoryMessage,
): Array<Record<string, unknown>> {
  const refs = item.knowledge_references ?? []
  // Only rebuild retrieval steps when citations prove a search actually ran.
  // Content-only turns (e.g. attachment Q&A with no KB hits) must not get a
  // fake "knowledge_search" row on history reload.
  if (refs.length === 0) {
    return []
  }

  const kbCounts: Record<string, number> = {}
  let docCount = 0
  let webCount = 0

  for (const ref of refs) {
    if (ref.chunk_type === 'web_search') {
      webCount++
      continue
    }
    docCount++
    const key = ref.knowledge_id || ref.knowledge_title || 'document'
    kbCounts[key] = (kbCounts[key] || 0) + 1
  }

  const searchSource = inferRetrievalSearchSource(refs)

  const events: Array<Record<string, unknown>> = [
    {
      type: 'tool_call',
      tool_call_id: 'rag-history-query-understand',
      tool_name: 'query_understand',
      pending: false,
      success: true,
    },
    {
      type: 'tool_call',
      tool_call_id: 'rag-history-knowledge-search',
      tool_name: 'knowledge_search',
      pending: false,
      success: true,
      arguments: { search_source: searchSource },
      tool_data: {
        count: refs.length,
        doc_count: docCount,
        web_count: webCount,
        search_source: searchSource,
        kb_counts: kbCounts,
        results: refs,
      },
    },
  ]

  return events
}

export function ensureRagPipelineHistoryStream(item: RagHistoryMessage & {
  content?: string
  is_completed?: boolean
  isAgentMode?: boolean
  hideContent?: boolean
}): void {
  if (!item.is_completed) return

  const stream = Array.isArray(item.agentEventStream)
    ? [...item.agentEventStream]
    : []

  if (hasRagPipelineToolEvents(stream)) return

  const hasRestorablePayload =
  Boolean(item.content?.trim()) ||
  Boolean(item.knowledge_references?.length)
  if (!hasRestorablePayload) return

  const synthesized = synthesizeRagPipelineToolEvents(item)
  const preserved = stream.filter((event) => {
    return !(
      event.type === 'tool_call' &&
      typeof event.tool_name === 'string' &&
      RAG_PIPELINE_TOOL_NAMES.has(event.tool_name)
    )
  })

  item.agentEventStream = [...synthesized, ...preserved]

  const hasAnswer = preserved.some((event) => {
    if (event.type !== 'answer' || event.superseded) return false
    const content = event.content
    return typeof content === 'string' && content.trim().length > 0
  })

  if (!hasAnswer && item.content?.trim()) {
    item.agentEventStream.push({
      type: 'answer',
      content: item.content,
      done: true,
    })
  }

  item.isAgentMode = true
  item.hideContent = true
}
