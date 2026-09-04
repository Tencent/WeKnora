import type { Chapter, ContentState, CurrentKnowledgeAnchor, CrossVideoKnowledgeItem, RelationOverview, SummarySection } from '@/types/videohub'

export interface RelatedKnowledgeContent {
  videoId: string
  overview: RelationOverview | null
  anchors: CurrentKnowledgeAnchor[]
  crossVideoItems: CrossVideoKnowledgeItem[]
}

export interface VideoContentState {
  outline: ContentState<Chapter[]>
  summary: ContentState<SummarySection[]>
  relatedKnowledge: ContentState<RelatedKnowledgeContent>
  transcriptPage: ContentState<string>
}

export type VideoContentModule = keyof VideoContentState

export function shouldShowRelatedKnowledgeTab(_state: ContentState<RelatedKnowledgeContent>): boolean {
  // Keep the entry point visible even before the knowledge layer has produced data.
  // An empty or unavailable state is rendered inside the tab instead of changing
  // the page structure after content loading completes.
  return true
}

export const emptyRelatedKnowledge: RelatedKnowledgeContent = {
  videoId: '',
  overview: null,
  anchors: [],
  crossVideoItems: [],
}

export function createLoadingContentState(): VideoContentState {
  return {
    outline: { status: 'loading', data: [] },
    summary: { status: 'loading', data: [] },
    relatedKnowledge: { status: 'loading', data: emptyRelatedKnowledge },
    transcriptPage: { status: 'loading', data: '' },
  }
}

export function createLoadingContentModuleState(module: VideoContentModule): VideoContentState[VideoContentModule] {
  if (module === 'outline') return { status: 'loading', data: [] }
  if (module === 'summary') return { status: 'loading', data: [] }
  if (module === 'relatedKnowledge') return { status: 'loading', data: emptyRelatedKnowledge }
  return { status: 'loading', data: '' }
}

export function contentModuleForStage(stage: string): VideoContentModule | 'all' | null {
  if (stage === 'outline') return 'outline'
  if (stage === 'summary') return 'summary'
  if (stage === 'graph') return 'relatedKnowledge'
  if (stage === 'related_knowledge') return 'relatedKnowledge'
  if (stage === 'assemble') return 'all'
  return null
}

function readErrorField(reason: unknown, field: string): string {
	if (!reason || typeof reason !== 'object') return ''
	const source = reason as Record<string, unknown>
	if (source[field] !== undefined && source[field] !== null) return String(source[field])
	const response = source.response
	if (!response || typeof response !== 'object') return ''
	const responseData = (response as Record<string, unknown>).data
	if (!responseData || typeof responseData !== 'object') return ''
	const value = (responseData as Record<string, unknown>)[field]
	return value === undefined || value === null ? '' : String(value)
}

function errorMessage(reason: unknown): string {
  if (reason instanceof Error) return reason.message
  return readErrorField(reason, 'error_message') || readErrorField(reason, 'message') || readErrorField(reason, 'error')
}

export function classifyContentError(reason: unknown): 'not_generated' | 'error' {
  const status = readErrorField(reason, 'status')
  const code = readErrorField(reason, 'error_code') || readErrorField(reason, 'code')
  const normalizedCode = code.toUpperCase()
  if (code === 'not_generated' || normalizedCode === 'CONTENT_NOT_READY') return 'not_generated'
  if (code === 'artifact_missing' || normalizedCode === 'CONTENT_NOT_FOUND') return 'error'
  if (status === '404') return 'not_generated'
  if (/wiki_page_id\s+not yet generated/i.test(errorMessage(reason))) return 'not_generated'
  return 'error'
}

export function settleContentState<T>(result: PromiseSettledResult<T>, emptyData: T, isEmpty: (data: T) => boolean): ContentState<T> {
  if (result.status === 'rejected') {
    const status = classifyContentError(result.reason)
    return {
      status,
      data: emptyData,
      ...(status === 'error' ? { error: errorMessage(result.reason) || '内容加载失败' } : {}),
    }
  }
  return { status: isEmpty(result.value) ? 'empty' : 'ready', data: result.value }
}

export function buildVideoContentState(
  outline: PromiseSettledResult<Chapter[]>,
  summary: PromiseSettledResult<{ sections: SummarySection[] }>,
  relatedKnowledge: PromiseSettledResult<RelatedKnowledgeContent>,
  transcriptPage: PromiseSettledResult<string> = { status: 'fulfilled', value: '' },
): VideoContentState {
  return {
    outline: settleContentState(outline, [], data => data.length === 0),
    summary: summary.status === 'fulfilled'
      ? { status: summary.value.sections.length === 0 ? 'empty' : 'ready', data: summary.value.sections }
      : settleContentState(summary, [], () => true),
    relatedKnowledge: settleContentState(relatedKnowledge, emptyRelatedKnowledge, data => data.anchors.length === 0 && data.crossVideoItems.length === 0),
    transcriptPage: settleContentState(transcriptPage, '', data => data.trim().length === 0),
  }
}
