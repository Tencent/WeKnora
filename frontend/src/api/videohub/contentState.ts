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
}

export type VideoContentModule = keyof VideoContentState

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
  }
}

export function createLoadingContentModuleState(module: VideoContentModule): VideoContentState[VideoContentModule] {
  if (module === 'outline') return { status: 'loading', data: [] }
  if (module === 'summary') return { status: 'loading', data: [] }
  return { status: 'loading', data: emptyRelatedKnowledge }
}

export function contentModuleForStage(stage: string): VideoContentModule | 'all' | null {
  if (stage === 'outline') return 'outline'
  if (stage === 'summary') return 'summary'
  if (stage === 'graph') return 'relatedKnowledge'
  if (stage === 'assemble') return 'all'
  return null
}

function readErrorField(reason: unknown, field: string): string {
  if (!reason || typeof reason !== 'object') return ''
  return String((reason as Record<string, unknown>)[field] || '')
}

function errorMessage(reason: unknown): string {
  if (reason instanceof Error) return reason.message
  return readErrorField(reason, 'error_message') || readErrorField(reason, 'message') || readErrorField(reason, 'error')
}

export function classifyContentError(reason: unknown): 'not_generated' | 'error' {
  const status = readErrorField(reason, 'status')
  const code = readErrorField(reason, 'error_code') || readErrorField(reason, 'code')
  if (status === '404' || code === 'not_generated' || code === 'artifact_missing') return 'not_generated'
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
): VideoContentState {
  return {
    outline: settleContentState(outline, [], data => data.length === 0),
    summary: summary.status === 'fulfilled'
      ? { status: summary.value.sections.length === 0 ? 'empty' : 'ready', data: summary.value.sections }
      : settleContentState(summary, [], () => true),
    relatedKnowledge: settleContentState(relatedKnowledge, emptyRelatedKnowledge, data => data.anchors.length === 0 && data.crossVideoItems.length === 0),
  }
}
