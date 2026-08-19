import type { VideoData, VideoOption } from '@/types/videohub'
import { MOCK_VIDEOS } from './mockVideos'

const MOCK_DELAY_MS = 120

function delay<T>(value: T): Promise<T> {
  return new Promise(resolve => window.setTimeout(() => resolve(value), MOCK_DELAY_MS))
}

export async function fetchVideoList(): Promise<VideoData[]> {
  return delay(MOCK_VIDEOS.map(video => ({ ...video })))
}

export async function fetchVideoDetail(id: string): Promise<VideoData> {
  const video = MOCK_VIDEOS.find(item => item.id === id)
  if (!video) {
    throw new Error('未找到该视频')
  }
  return delay({ ...video })
}

export async function fetchVideoOptions(): Promise<VideoOption[]> {
  return delay(MOCK_VIDEOS.map(({ id, title }) => ({ id, title })))
}
