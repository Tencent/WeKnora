import { get, post, postUpload } from '@/utils/request'
import type { VideoData, VideoOption, VideoProcessingStatus } from '@/types/videohub'
import { isVideoInitiallyAvailable, mapVideo } from './videoMapping'
import { parseSubtitleFile } from './contentParsing'

export {
  buildVideoContentState,
  classifyContentError,
  contentModuleForStage,
  createLoadingContentModuleState,
  createLoadingContentState,
  fetchVideoContent,
  fetchVideoContentModule,
  type VideoContentModule,
  type VideoContentState,
} from './contentLoader'

export { isVideoInitiallyAvailable, mapVideo } from './videoMapping'
export { parseSubtitleFile } from './contentParsing'

export async function fetchVideoList(): Promise<VideoData[]> {
  const resp: any = await get('/api/custom/videos')
  return (resp?.data || []).map(mapVideo)
}

export async function fetchVideoDetail(id: string): Promise<VideoData> {
  const resp: any = await get(`/api/custom/videos/${id}`)
  return mapVideo(resp?.data, resp)
}

export async function fetchVideoSubtitles(url: string): Promise<ReturnType<typeof parseSubtitleFile>> {
  try {
    const subtitleText = await get<string>(url, { responseType: 'text' })
    return parseSubtitleFile(subtitleText)
  } catch {
    return []
  }
}

export async function fetchVideoOptions(): Promise<VideoOption[]> {
  const resp: any = await get('/api/custom/videos')
  return (resp?.data || []).map((v: any) => ({ id: v.id, title: v.title }))
}

export async function fetchVideoProcessingStatus(id: string): Promise<VideoProcessingStatus> {
  return get(`/api/custom/videos/${id}/processing-status`)
}

export async function retryVideoProcessingStage(id: string, jobType: string): Promise<{ job_id: string; job_type: string; status: string; reused: boolean }> {
  return post(`/api/custom/videos/${id}/processing-jobs/${jobType}/retry`)
}

export async function importVideoTranscript(videoId: string, file: File): Promise<{
  video_id: string
  status: string
  source: string
  reused: boolean
  index_job_id?: string
  subtitle_count?: number
}> {
  const formData = new FormData()
  formData.append('file', file, file.name)
  return postUpload(`/api/custom/videos/${videoId}/transcript/import`, formData)
}
