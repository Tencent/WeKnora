import type { ComposerTranslation } from 'vue-i18n'

function collectQueryStrings(value: unknown): string[] {
  if (value == null) return []

  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return []
    if (trimmed.startsWith('[')) {
      try {
        const parsed = JSON.parse(trimmed)
        if (Array.isArray(parsed)) {
          return parsed.filter((q): q is string => typeof q === 'string' && Boolean(q.trim()))
        }
      } catch {
        // fall through to treat as a single query string
      }
    }
    return [trimmed]
  }

  if (Array.isArray(value)) {
    return value.filter((q): q is string => typeof q === 'string' && Boolean(q.trim()))
  }

  return []
}

export function getQueryText(args: unknown): string {
  if (!args) return ''

  let parsedArgs = args
  if (typeof parsedArgs === 'string') {
    try {
      parsedArgs = JSON.parse(parsedArgs)
    } catch {
      return ''
    }
  }

  if (!parsedArgs || typeof parsedArgs !== 'object') return ''

  const queries: string[] = []
  const record = parsedArgs as Record<string, unknown>

  queries.push(...collectQueryStrings(record.query))
  queries.push(...collectQueryStrings(record.queries))

  return Array.from(new Set(queries)).join('，')
}

export function getWikiPageText(args: unknown): string {
  if (!args) return ''

  let parsedArgs = args
  if (typeof parsedArgs === 'string') {
    try {
      parsedArgs = JSON.parse(parsedArgs)
    } catch {
      return ''
    }
  }

  if (!parsedArgs || typeof parsedArgs !== 'object') return ''

  const record = parsedArgs as Record<string, unknown>
  const slugs = [
    ...collectQueryStrings(record.slug),
    ...collectQueryStrings(record.slugs),
  ]
  return Array.from(new Set(slugs)).join('、')
}

export function getKnowledgeSearchSummaryHtml(
  t: ComposerTranslation,
  toolData: Record<string, unknown> | null | undefined,
): string {
  if (!toolData) return ''

  const results = toolData.results
  const count = (Array.isArray(results) ? results.length : 0) || Number(toolData.count) || 0
  if (count === 0) return t('agentStream.search.noResults')

  const kbCounts = toolData.kb_counts
  const kbCount = kbCounts && typeof kbCounts === 'object' ? Object.keys(kbCounts).length : 0
  if (kbCount > 0) {
    return t('agentStream.search.foundResultsFromFiles', {
      count: `<strong>${count}</strong>`,
      files: `<strong>${kbCount}</strong>`,
    })
  }

  return t('agentStream.search.foundResults', { count: `<strong>${count}</strong>` })
}

type RagPipelineEvent = {
  tool_name?: string
  pending?: boolean
  success?: boolean
  arguments?: unknown
  tool_data?: Record<string, unknown> | null
}

export function getRagPipelineStepTitle(t: ComposerTranslation, event: RagPipelineEvent): string {
  const toolName = String(event.tool_name || '')
  const pending = event.pending === true
  const query =
    getQueryText(event.arguments) ||
    getQueryText(event.tool_data)

  if (toolName === 'query_understand') {
    return pending
      ? t('agentStream.toolStatus.queryUnderstanding')
      : t('agentStream.toolStatus.queryUnderstandDone')
  }

  if (toolName === 'knowledge_search' || toolName === 'search_knowledge') {
    if (pending) {
      return query
        ? t('agentStream.ragPipeline.searchingWithQuery', { query })
        : t('agentStream.ragPipeline.searching')
    }

    const baseTitle = event.success === false
      ? t('agentStream.toolStatus.searchKbFailed')
      : t('agentStream.toolStatus.searchKb')
    return query ? `${baseTitle}：「${query}」` : baseTitle
  }

  return ''
}
