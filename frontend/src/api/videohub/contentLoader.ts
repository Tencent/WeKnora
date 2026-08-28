import { fetchOutline } from './outline'
import { fetchRelatedKnowledge } from './relatedKnowledge'
import { fetchSummary } from './summary'
import { fetchOverview } from './overview'
import { fetchTranscriptPage } from './transcriptPage'
import { buildVideoContentState, emptyRelatedKnowledge, settleContentState, type VideoContentModule, type VideoContentState } from './contentState'
import type { VideoCategory } from '@/types/videohub'

export { buildVideoContentState, classifyContentError, createLoadingContentState, type VideoContentState } from './contentState'
export { contentModuleForStage, createLoadingContentModuleState, type VideoContentModule } from './contentState'

export async function fetchVideoContentModule(
  videoId: string,
  durationSeconds: number,
  category: VideoCategory,
  module: VideoContentModule,
): Promise<VideoContentState[VideoContentModule]> {
  if (module === 'outline') {
    const result = await Promise.allSettled([fetchOutline(videoId, durationSeconds)])
    return settleContentState(result[0], [], data => data.length === 0)
  }
  if (module === 'overview') {
    const result = await Promise.allSettled([fetchOverview(videoId)])
    return settleContentState(result[0], '', data => data.trim().length === 0)
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
  const [outline, overview, summary, relatedKnowledge, transcriptPage] = await Promise.allSettled([
    fetchOutline(videoId, durationSeconds),
    fetchOverview(videoId),
    fetchSummary(videoId, category),
    fetchRelatedKnowledge(videoId),
    fetchTranscriptPage(videoId),
  ])

  return buildVideoContentState(outline, summary, relatedKnowledge, overview, transcriptPage)
}
