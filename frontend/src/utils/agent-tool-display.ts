import type { ComposerTranslation } from 'vue-i18n'

export type RetrievalSearchSource = 'knowledge' | 'web' | 'mixed'

const THINKING_UUID_RE = /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi
const THINKING_INTERNAL_ID_RE = /(?:(?:source[_\s-]*(?:video|document)|video|knowledge(?:[_\s-]*(?:base|document))?|kb|chunk|document|file|task|job|run|request|trace|transcript)[_\s-]*id(?:s)?\b|(?:视频|知识库|文档|分块|文件|任务|作业|请求|会话|运行)[\s_-]*(?:id|编号))\s*(?:(?:[:：=]\s*|(?:is|为|是)\s*)[`'"“「]?(?:[^\s,，。；;、)）\]}]+)[`'"”」]?)?/gi
const THINKING_GENERATION_RE = /(?:\b(?:transcript|transcription|transcoding)[_\s-]*generation\b|(?:转码|转写)\s*(?:代次|版本))\s*(?:(?:[:：=]\s*|(?:is|为|是)\s*)[`'"“「]?(?:[^\s,，。；;、)）\]}]+)[`'"”」]?)?/gi
const THINKING_PATH_FIELD_RE = /(?:\b(?:file|source|document)?[_\s-]*path\b|文件(?:路径|地址)|路径)\s*(?:(?:[:：=]\s*|(?:is|为|是)\s*)(?:"[^"\n]*"|'[^'\n]*'|`[^`\n]*`|[^\s,，。；;、)）\]}]+))?/gi
const THINKING_ABSOLUTE_PATH_RE = /(?:\/(?:Users|var|tmp|home|private|opt|mnt|workspace|app|srv)\/[^\s`'"，。；;、)）\]}]+|\b[A-Za-z]:[\\/][^\s`'"，。；;、)）\]}]+)/gi
const THINKING_RELATIVE_PATH_RE = /(?:^|(?<=\s))(?:\.\.?\/|(?:frontend|src|internal|cmd|dist|output|tmp|var)\/)[^\s`'"，。；;、)）\]}]+/gim
const THINKING_FILE_RE = /`(?:[^`\n]*[\\/][^`\n]*|[^`\n]+\.(?:md|html?|json|ya?ml|ts|tsx|vue|png|jpe?g|pdf))`/gi

function collapseRepeatedThinkingBlock(value: string): string {
  const leading = value.match(/^\s*/)?.[0] || ''
  const trailing = value.match(/\s*$/)?.[0] || ''
  const core = value.trim()
  if (core.length < 8) return value

  for (let repeatCount = 2; repeatCount <= 4; repeatCount += 1) {
    if (core.length % repeatCount !== 0) continue
    const blockLength = core.length / repeatCount
    const block = core.slice(0, blockLength)
    if (block.repeat(repeatCount) === core) {
      return `${leading}${block}${trailing}`
    }
  }
  return value
}

function dedupeThinkingSegments(value: string): string {
  const segments = value.match(/(?:[^\n。！？.!?]+[。！？.!?]?|\n+)/g) || [value]
  const seen = new Set<string>()
  const deduped = segments.filter((segment) => {
    if (/^\s+$/.test(segment)) return true
    const normalized = segment.trim().replace(/\s+/g, ' ')
    if (!normalized || seen.has(normalized)) return false
    seen.add(normalized)
    return true
  })
  return deduped.join('').replace(/\n{3,}/g, '\n\n')
}

/** Remove implementation identifiers from model reasoning before it reaches the UI. */
export function sanitizeThinkingText(value: string): string {
  if (!value) return value

  let result = value
    .replace(THINKING_INTERNAL_ID_RE, '')
    .replace(THINKING_GENERATION_RE, '')
    .replace(THINKING_UUID_RE, '')
    .replace(THINKING_PATH_FIELD_RE, '')
    .replace(THINKING_ABSOLUTE_PATH_RE, '')
    .replace(THINKING_RELATIVE_PATH_RE, '')
    .replace(THINKING_FILE_RE, '')
    .replace(/\(\s*\)/g, '')
    .replace(/[ \t]{2,}/g, ' ')
    .replace(/[ \t]+([，。；：！？])/g, '$1')

  // Streaming can append the same reasoning sentence more than once. Keep
  // one copy without flattening newlines, lists, or other Markdown structure.
  result = collapseRepeatedThinkingBlock(result)
  result = dedupeThinkingSegments(result)
  result = result
    .replace(/^[ \t]*[，。；：！？,.!?]+[ \t]*/gm, '')
    .replace(/([，。；：！？,.!?])[ \t]*(?=[，。；：！？,.!?])/g, '$1')

  return result.trim()
}

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

export function getRetrievalSearchSource(
  args: unknown,
  toolData?: Record<string, unknown> | null,
): RetrievalSearchSource {
  const fromArgs =
    args && typeof args === 'object'
      ? String((args as Record<string, unknown>).search_source || '')
      : ''
  const fromData = toolData ? String(toolData.search_source || '') : ''
  const source = (fromData || fromArgs).trim()
  if (source === 'web' || source === 'mixed') {
    return source
  }
  return 'knowledge'
}

function getRetrievalStatusKeys(source: RetrievalSearchSource, failed: boolean) {
  if (source === 'web') {
    return failed
      ? { pending: 'agentStream.ragPipeline.searchingWeb', pendingWithQuery: 'agentStream.ragPipeline.searchingWebWithQuery', done: 'agentStream.toolStatus.webSearch', doneFailed: 'agentStream.toolStatus.webSearchFailed' }
      : { pending: 'agentStream.ragPipeline.searchingWeb', pendingWithQuery: 'agentStream.ragPipeline.searchingWebWithQuery', done: 'agentStream.toolStatus.webSearch', doneFailed: 'agentStream.toolStatus.webSearchFailed' }
  }
  if (source === 'mixed') {
    return {
      pending: 'agentStream.ragPipeline.searchingMixed',
      pendingWithQuery: 'agentStream.ragPipeline.searchingMixedWithQuery',
      done: 'agentStream.toolStatus.searchMixed',
      doneFailed: 'agentStream.toolStatus.searchMixedFailed',
    }
  }
  return {
    pending: 'agentStream.ragPipeline.searching',
    pendingWithQuery: 'agentStream.ragPipeline.searchingWithQuery',
    done: 'agentStream.toolStatus.searchKb',
    doneFailed: 'agentStream.toolStatus.searchKbFailed',
  }
}

export function getKnowledgeSearchSummaryHtml(
  t: ComposerTranslation,
  toolData: Record<string, unknown> | null | undefined,
): string {
  if (!toolData) return ''

  const results = toolData.results
  const count = (Array.isArray(results) ? results.length : 0) || Number(toolData.count) || 0
  if (count === 0) {
    // Retrieval found candidates but none cleared the relevance threshold, so the
    // turn answered from the fallback with nothing retrieved in its context. Say
    // that rather than "no matching content": the difference is what tells you to
    // look at the threshold instead of the knowledge base.
    const candidateCount = Number(toolData.candidate_count) || 0
    if (candidateCount > 0) {
      return t('agentStream.search.candidatesBelowThreshold', {
        count: `<strong>${candidateCount}</strong>`,
      })
    }
    return t('agentStream.search.noResults')
  }

  const searchSource = getRetrievalSearchSource(null, toolData)
  const webCount = Number(toolData.web_count) || 0
  const docCount = Number(toolData.doc_count) || 0

  if (searchSource === 'web' || (webCount > 0 && docCount === 0)) {
    return t('agentStream.search.webResults', { count: `<strong>${count}</strong>` })
  }

  const kbCounts = toolData.kb_counts
  const kbCount = kbCounts && typeof kbCounts === 'object' ? Object.keys(kbCounts).length : 0
  if (kbCount > 0) {
    return t('agentStream.search.foundResultsFromFiles', {
      count: `<strong>${count}</strong>`,
      files: `<strong>${kbCount}</strong>`,
    })
  }

  if (searchSource === 'mixed' && docCount > 0 && webCount > 0) {
    return t('agentStream.search.foundMixedResults', {
      count: `<strong>${count}</strong>`,
      docCount: `<strong>${docCount}</strong>`,
      webCount: `<strong>${webCount}</strong>`,
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
    const searchSource = getRetrievalSearchSource(event.arguments, event.tool_data)
    const labels = getRetrievalStatusKeys(searchSource, event.success === false)
    if (pending) {
      return query
        ? t(labels.pendingWithQuery, { query })
        : t(labels.pending)
    }

    const baseTitle = event.success === false ? t(labels.doneFailed) : t(labels.done)
    return query ? `${baseTitle}：「${query}」` : baseTitle
  }

  if (toolName === 'attachment_parsing') {
    if (pending) return t('agentStream.toolStatus.attachmentParsing')
    return event.success === false
      ? t('agentStream.toolStatus.attachmentParsingFailed')
      : t('agentStream.toolStatus.attachmentParsingDone')
  }

  if (toolName === 'image_analysis') {
    if (pending) return t('agentStream.toolStatus.imageAnalyzing')
    return event.success === false
      ? t('agentStream.toolStatus.imageAnalysisFailed')
      : t('agentStream.toolStatus.imageAnalysisDone')
  }

  return ''
}
