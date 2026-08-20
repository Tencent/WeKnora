import { MOCK_VIDEOS } from './mockVideos'
import type { SummarySection, VideoCategory } from '@/types/videohub'

export interface SummaryResponse {
  videoId: string
  category: VideoCategory
  sections: SummarySection[]
}

export async function fetchSummary(videoId: string): Promise<SummaryResponse> {
  const video = MOCK_VIDEOS.find(item => item.id === videoId)
  if (!video) throw new Error('视频不存在')

  const sections = video.category === 'interview'
    ? video.interviewSummary
    : video.category === 'training'
      ? video.trainingSummary
      : video.category === 'salon'
        ? video.salonSummary
        : video.summarySections

  return { videoId, category: video.category, sections: sections ?? [] }
}
