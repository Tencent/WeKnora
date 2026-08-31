interface WeKnoraStreamChunk {
  response_type?: string
  type?: string
  content?: string
  data?: Record<string, unknown>
  done?: boolean
}

export interface ParsedStreamChunk {
  kind: 'answer' | 'thinking' | 'activity' | 'complete' | 'error' | 'ignore'
  content?: string
  done?: boolean
}

function chunkType(data: WeKnoraStreamChunk) {
  return String(data.response_type || data.type || '').trim()
}

function streamErrorMessage(data: WeKnoraStreamChunk) {
  return String(data.content || data.data?.error || data.data?.message || '问答生成失败')
}

function completeAnswer(data: WeKnoraStreamChunk) {
  return typeof data.data?.final_answer === 'string' ? data.data.final_answer : ''
}

function toolDisplayName(value: unknown) {
  const name = String(value || '').trim()
  const labels: Record<string, string> = {
    search_knowledge: '检索知识库',
    knowledge_search: '检索知识库',
    list_knowledge_chunks: '读取知识片段',
    get_document_info: '读取文档信息',
    get_document_content: '读取文档内容',
    wiki_search: '检索 Wiki',
    wiki_read_page: '读取 Wiki 页面',
    web_search: '检索网页',
    thinking: '思考',
  }
  return labels[name] || '工具'
}

function activityFromChunk(type: string, data: WeKnoraStreamChunk) {
  const payload = data.data || {}
  if (type === 'tool_call') {
    const toolName = toolDisplayName(payload.tool_name || payload.name)
    return `正在调用 ${toolName}`
  }
  if (type === 'tool_result') {
    const toolName = toolDisplayName(payload.tool_name || payload.name)
    return payload.success === false ? `${toolName} 调用失败` : `${toolName} 调用完成`
  }
  if (type === 'agent_query') return '正在检索视频知识'
  if (type === 'references' || type === 'memory_recalled') return '已找到相关知识'
  return ''
}

function markDone<T extends ParsedStreamChunk>(chunk: T, data: WeKnoraStreamChunk): T {
  return data.done === true ? { ...chunk, done: true } : chunk
}

export function parseWeKnoraStreamChunk(raw: string): ParsedStreamChunk {
  if (!raw) return { kind: 'ignore' }
  if (raw === '[DONE]') return { kind: 'complete', done: true }
  let data: WeKnoraStreamChunk
  try {
    data = JSON.parse(raw)
  } catch {
    return { kind: 'ignore' }
  }

  const type = chunkType(data)
  if (type === 'error') return markDone({ kind: 'error', content: streamErrorMessage(data) }, data)
  if (type === 'answer') return markDone({ kind: 'answer', content: String(data.content || '') }, data)
  if (type === 'thinking' || type === 'reflection') return markDone({ kind: 'thinking', content: String(data.content || '') }, data)
  if (type === 'complete' || (!type && data.done)) return { kind: 'complete', content: completeAnswer(data), done: true }

  const activity = activityFromChunk(type, data)
  return activity ? markDone({ kind: 'activity', content: activity }, data) : markDone({ kind: 'ignore' }, data)
}
