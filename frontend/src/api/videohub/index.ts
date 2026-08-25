import { get } from '@/utils/request'
import type { VideoData, VideoOption } from '@/types/videohub'
import { isVideoInitiallyAvailable, mapVideo } from './videoMapping'

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
