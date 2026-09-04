import { fetchOutlineResult } from './outline'
import { fetchRelatedKnowledge } from './relatedKnowledge'
import { fetchSummary } from './summary'
import { fetchTranscriptPage } from './transcriptPage'
import { buildVideoContentState, emptyRelatedKnowledge, settleContentState, type VideoContentModule, type VideoContentState } from './contentState'
import type { Chapter, VideoCategory } from '@/types/videohub'

export { buildVideoContentState, classifyContentError, createLoadingContentState, type VideoContentState } from './contentState'
export { contentModuleForStage, createLoadingContentModuleState, type VideoContentModule } from './contentState'

export async function fetchVideoContentModule(
  videoId: string,
  durationSeconds: number,
  category: VideoCategory,
  module: VideoContentModule,
): Promise<VideoContentState[VideoContentModule]> {
  if (module === 'outline') {
    const result = await Promise.allSettled([fetchOutlineResult(videoId, durationSeconds)])
    if (result[0].status === 'fulfilled') {
      const value = result[0].value
      return { status: value.partial ? 'partial' : (value.chapters.length === 0 ? 'empty' : 'ready'), data: value.chapters }
    }
    return settleContentState(result[0], [], () => true)
  }
  if (module === 'summary') {
    const result = await Promise.allSettled([fetchSummary(videoId, category)])
    if (result[0].status === 'fulfilled') {
      return { status: result[0].value.sections.length === 0 ? 'empty' : 'ready', data: result[0].value.sections }
    }
    return settleContentState(result[0], [], () => true)
  }
  if (module === 'relatedKnowledge') {
    const result = await Promise.allSettled([fetchRelatedKnowledge(videoId)])
    return settleContentState(result[0], emptyRelatedKnowledge, data => data.anchors.length === 0 && data.crossVideoItems.length === 0)
  }
  const result = await Promise.allSettled([fetchTranscriptPage(videoId)])
  return settleContentState(result[0], '', data => data.trim().length === 0)
}

export async function fetchVideoContent(
  videoId: string,
  durationSeconds: number,
  category: VideoCategory,
): Promise<VideoContentState> {
  const [outline, summary, relatedKnowledge, transcriptPage] = await Promise.allSettled([
    fetchOutlineResult(videoId, durationSeconds),
    fetchSummary(videoId, category),
    fetchRelatedKnowledge(videoId),
    fetchTranscriptPage(videoId),
  ])

  const outlineState: PromiseSettledResult<Chapter[]> = outline.status === 'fulfilled'
    ? { status: 'fulfilled', value: outline.value.chapters }
    : outline
  const state = buildVideoContentState(outlineState, summary, relatedKnowledge, transcriptPage)
  if (outline.status === 'fulfilled' && outline.value.partial) state.outline.status = 'partial'
  return state
}
