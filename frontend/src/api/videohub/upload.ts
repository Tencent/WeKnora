import { MOCK_VIDEOS } from './mockVideos'
import type { UploadForm, VideoData } from '@/types/videohub'

export interface UploadCallbacks { onProgress(percent: number): void }
export interface UploadCancel { cancelled: boolean }

export class UploadCancelledError extends Error {
  constructor() {
    super('上传已取消')
    this.name = 'UploadCancelledError'
  }
}

function delay(milliseconds: number) {
  return new Promise(resolve => window.setTimeout(resolve, milliseconds))
}

function localDateTime() {
  const date = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export async function uploadVideo(form: UploadForm, callbacks: UploadCallbacks, cancel: UploadCancel): Promise<VideoData> {
  const videoName = form.file.name
  const shouldFail = localStorage.getItem('videohub.failNextUpload') === '1'
  if (shouldFail) localStorage.removeItem('videohub.failNextUpload')

  let percent = 0
  callbacks.onProgress(percent)
  while (percent < 100) {
    await delay(140)
    if (cancel.cancelled) throw new UploadCancelledError()
    percent = Math.min(100, percent + 10 + Math.floor(Math.random() * 6))
    callbacks.onProgress(percent)
    if (shouldFail && percent >= 58) throw new Error('模拟上传失败，请检查网络后重试')
  }

  if (cancel.cancelled) throw new UploadCancelledError()
  const source = MOCK_VIDEOS[0]
  const id = `video-${Date.now()}`
  const video: VideoData = {
    id,
    title: videoName,
    category: 'general',
    categoryName: '通用分享',
    duration: '18:25',
    durationSeconds: 1105,
    created_at: localDateTime(),
    video_url: source?.video_url || '',
    poster_url: source?.poster_url,
    overview: `已上传视频“${videoName}”的知识概览。`,
    chapters: [{
      id: `${id}-chapter-1`, chapter_index: '01', chapter_title: '内容概览',
      start_time: '00:00', start_seconds: 0, end_time: '18:25', end_seconds: 1105,
      chapter_summary: '本章概括视频主题、关键观点与可执行建议。',
      knowledge_points: [
        { id: `${id}-kp-1`, title: '主题与背景', timestamp: '00:20', seconds: 20, transcriptSnippet: '视频首先介绍主题背景与核心问题。' },
        { id: `${id}-kp-2`, title: '关键行动建议', timestamp: '06:30', seconds: 390, transcriptSnippet: '结合实际场景给出可执行的行动建议。' },
      ],
    }],
    subtitles: [
      { start_seconds: 0, end_seconds: 8, text: `欢迎观看《${videoName}》。` },
      { start_seconds: 8, end_seconds: 18, text: '接下来先介绍视频主题与背景。' },
      { start_seconds: 18, end_seconds: 30, text: '我们会逐步拆解关键观点和方法。' },
      { start_seconds: 30, end_seconds: 45, text: '你可以通过章节导航快速定位内容。' },
    ],
  }
  MOCK_VIDEOS.unshift(video)
  return video
}
