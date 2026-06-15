export const RAG_PIPELINE_TOOL_NAMES = new Set(['query_understand', 'knowledge_search'])

type RagHistoryMessage = {
  knowledge_references?: Array<{
    chunk_type?: string
    knowledge_id?: string
    knowledge_title?: string
  }>
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
  const kbCounts: Record<string, number> = {}

  for (const ref of refs) {
    if (ref.chunk_type === 'web_search') continue
    const key = ref.knowledge_id || ref.knowledge_title || 'document'
    kbCounts[key] = (kbCounts[key] || 0) + 1
  }

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
      tool_data: {
        count: refs.length,
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
