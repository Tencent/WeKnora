import type { ComposerTranslation } from 'vue-i18n'

const labels = {
  find_operations: 'findOperations',
  run_operation: 'runOperation',
  search_weknora_docs: 'searchDocs',
  read_weknora_doc: 'readDoc',
} as const

export type AssistantToolDisplayType = keyof typeof labels

export function getAssistantToolDisplayType(name?: string): AssistantToolDisplayType | undefined {
  return name && Object.hasOwn(labels, name) ? name as AssistantToolDisplayType : undefined
}

export function toolRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown> : {}
}

export function toolText(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function toolRows(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.filter(item => Object.keys(toolRecord(item)).length).map(toolRecord) : []
}

// These tools store JSON in output. Live SSE also copies its event envelope to
// tool_data, so do not let envelope fields replace the result or its error.
export function parseAssistantToolOutput(output?: string, data?: unknown): Record<string, unknown> {
  let parsed: Record<string, unknown> = {}
  try { parsed = toolRecord(JSON.parse(output || '')) } catch { /* Plain errors or truncated history. */ }
  const extra = toolRecord(data)
  for (const key of ['operations', 'unavailable', 'hint', 'operation', 'result', 'pages', 'results',
    'path', 'title', 'category', 'url', 'anchor', 'section', 'content', 'outline', 'truncated', 'offset', 'next_offset']) {
    if (key in extra) parsed[key] = extra[key]
  }
  return parsed
}

export function assistantDocURL(value: unknown, anchor?: unknown): string | undefined {
  try {
    const url = new URL(toolText(value))
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return undefined
    if (typeof anchor === 'string') url.hash = anchor
    return url.href
  } catch { return undefined }
}

export function getAssistantToolTitle(t: ComposerTranslation, event: {
  tool_name?: string; arguments?: unknown; output?: string; tool_data?: unknown; pending?: boolean; success?: boolean
}): string {
  const type = getAssistantToolDisplayType(event.tool_name)
  if (!type) return ''
  const data = parseAssistantToolOutput(event.output, event.tool_data)
  const args = typeof event.arguments === 'string' ? parseAssistantToolOutput(event.arguments) : toolRecord(event.arguments)
  const detail = type === 'run_operation' ? data.operation || args.operation
    : type === 'read_weknora_doc' ? data.section || data.title || args.path
    : args.query || args.domain || args.category
  const label = t(`agentStream.assistantTools.${labels[type]}`)
  const title = toolText(detail) ? `${label}: ${detail}` : label
  if (event.pending) return t('agentStream.toolStatus.calling', { name: title })
  if (event.success === false) return t('agentStream.toolStatus.calledFailed', { name: title })
  return title
}
