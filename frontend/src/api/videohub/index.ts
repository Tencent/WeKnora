import { get, post } from '@/utils/request'
import type { VideoData, VideoOption, VideoProcessingStatus } from '@/types/videohub'
import { isVideoInitiallyAvailable, mapVideo } from './videoMapping'

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

export async function fetchVideoList(): Promise<VideoData[]> {
  const resp: any = await get('/api/custom/videos')
  return (resp?.data || []).map(mapVideo)
}

export async function fetchVideoDetail(id: string): Promise<VideoData> {
  const resp: any = await get(`/api/custom/videos/${id}`)
  return mapVideo(resp?.data, resp)
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
