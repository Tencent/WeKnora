import type { UploadForm, VideoData } from '@/types/videohub'

export interface UploadCallbacks { onProgress(percent: number): void }
export interface UploadCancel { cancelled: boolean }
interface GeneratedPoster {
  blob: Blob
  durationSeconds: number
}

export class UploadCancelledError extends Error {
  constructor() {
    super('上传已取消')
    this.name = 'UploadCancelledError'
  }
}

// 分片大小：5 MB（MinIO multipart 最小限制；过小会导致签名请求过多）
const PART_SIZE = 5 * 1024 * 1024
// 同源后端代理会占用服务端连接，生产环境保持较低并发以降低排队和超时概率。
const MAX_CONCURRENCY = 2
// 单片最大重试次数：网络抖动时重传，避免整文件从头再来
const MAX_RETRIES = 3
const RETRY_BACKOFF_MS = 700
const PART_TIMEOUT_MS = 5 * 60 * 1000

// 分片直传架构（VP-T002）：
// 1) POST /api/custom/uploads/multipart/init 拿 upload_id + video_id + object_key
// 2) 切片 + 并发 PUT 到同源后端（后端在内网写入 MinIO）
// 3) POST /api/custom/uploads/multipart/complete 后端合并 + 写库 + 入 thumbnail job
//
// 与单次直传相比：
//   - 每片 5MB 几秒完成，onprogress 触发粒度精细，消除"卡 90%"黑盒
//   - 失败只需重传该片，不用从头再来
//   - 支持任意大文件（不再受单次 PUT 浏览器进度反馈限制）
//
// 失败/取消时自动调用 multipart/abort 清理 MinIO 残留分片，避免存储泄漏。
export function uploadVideo(
  form: UploadForm,
  callbacks: UploadCallbacks,
  cancel: UploadCancel,
): Promise<VideoData> {
  const file = ((form as any).file?.raw || (form as any).file) as File
  if (!file || !file.name) throw new Error('请选择视频文件')

  const videoType = 'tutorial'

  // 提到外层以便 catch 调 abort 清理
  let init: { video_id: string; object_key: string; upload_id: string } | undefined
  let multipartCompleted = false

  callbacks.onProgress(2)

  return (async () => {
    // 步骤 1：init —— 后端创建 multipart upload + 占位 video 记录
    const initRes = await fetch('/api/custom/uploads/multipart/init', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        filename: file.name,
        content_type: file.type || 'application/octet-stream',
        video_type: videoType,
      }),
    })
    if (cancel.cancelled) throw new UploadCancelledError()
    if (!initRes.ok) {
      const data = await initRes.json().catch(() => ({} as { error?: string }))
      throw new Error(data?.error || `初始化分片上传失败（HTTP ${initRes.status}）`)
    }
    init = await initRes.json() as typeof init

    // 步骤 2：切片 + 并发上传
    const totalParts = Math.max(1, Math.ceil(file.size / PART_SIZE))
    const uploadedBytes = { count: 0 }
    const completedParts: { part_number: number; etag: string }[] = []
    // 跟踪 in-flight XHR，用户取消时统一 abort
    const inFlightXhrs: XMLHttpRequest[] = []

    const partIndexes = Array.from({ length: totalParts }, (_, i) => i)
    await runPool(partIndexes, MAX_CONCURRENCY, async (partIdx) => {
      if (cancel.cancelled) throw new UploadCancelledError()

      const partNumber = partIdx + 1
      const start = partIdx * PART_SIZE
      const end = Math.min(start + PART_SIZE, file.size)
      const blob = file.slice(start, end)

      // 单片重试 MAX_RETRIES 次
      let lastErr: Error | undefined
      for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
        if (cancel.cancelled) throw new UploadCancelledError()
        try {
          const etag = await putPartViaXhr(
            '/api/custom/uploads/multipart/part',
            blob,
            cancel,
            inFlightXhrs,
            {
              'X-Video-ID': init!.video_id,
              'X-Object-Key': init!.object_key,
              'X-Upload-ID': init!.upload_id,
              'X-Part-Number': String(partNumber),
            },
          )
          completedParts.push({ part_number: partNumber, etag })
          uploadedBytes.count += (end - start)
          // 进度映射：2% → 95%（按已上传字节比例推进，每片完成立即触发）
          const pct = 2 + Math.floor((uploadedBytes.count / file.size) * 93)
          callbacks.onProgress(Math.min(pct, 95))
          return // 成功，退出重试循环
        } catch (err) {
          if (err instanceof UploadCancelledError) throw err
          lastErr = err as Error
          if (attempt < MAX_RETRIES) {
            await new Promise<void>(r => setTimeout(r, RETRY_BACKOFF_MS))
          }
        }
      }
      // 重试耗尽，抛错（外层 catch 会调 abort 清理）
      throw new Error(`分片 ${partNumber}/${totalParts} 上传失败：${lastErr?.message ?? '未知错误'}`)
    })

    if (cancel.cancelled) throw new UploadCancelledError()

    // 步骤 3：complete —— 后端合并分片 + 入初始处理任务
    callbacks.onProgress(97)
    const completeRes = await fetch('/api/custom/uploads/multipart/complete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        video_id: init!.video_id,
        object_key: init!.object_key,
        upload_id: init!.upload_id,
        parts: completedParts,
      }),
    })
    if (cancel.cancelled) throw new UploadCancelledError()
    if (!completeRes.ok) {
      const data = await completeRes.json().catch(() => ({} as { error?: string }))
      throw new Error('合并分片失败：' + (data?.error || `HTTP ${completeRes.status}`))
    }
    multipartCompleted = true
    const completed = await completeRes.json() as { video_id: string; uploaded_at: string }

    const poster = await generateVideoPoster(file).catch(() => null)
    if (poster) {
      await uploadVideoPoster(completed.video_id, poster).catch(() => {})
    }

    await waitForVideoReady(completed.video_id, cancel, callbacks)
    callbacks.onProgress(100)

    // UploadModal 上传成功后调 afterUpload 刷新列表，video_url 由列表/详情接口补全
    return {
      id: completed.video_id,
      title: file.name.replace(/\.[^.]+$/, ''),
      category: 'training',
      categoryName: '培训',
      duration: formatDuration(poster.durationSeconds),
      durationSeconds: Math.floor(poster.durationSeconds),
      created_at: completed.uploaded_at || new Date().toISOString(),
      video_url: '',
      poster_url: '',
      overview: '',
      chapters: [],
      subtitles: [],
      summarySections: [],
    }
  })().catch(async (err) => {
    // 失败或取消：清理 MinIO 残留分片，避免存储泄漏
    if (init?.upload_id && !multipartCompleted) {
      await fetch('/api/custom/uploads/multipart/abort', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          video_id: init.video_id,
          object_key: init.object_key,
          upload_id: init.upload_id,
        }),
      }).catch(() => {}) // 静默失败：清理失败不阻塞错误传播
    }
    throw err
  }) as Promise<VideoData>
}

async function uploadVideoPoster(videoId: string, poster: GeneratedPoster): Promise<void> {
  const query = poster.durationSeconds > 0
    ? `?duration_seconds=${encodeURIComponent(Math.floor(poster.durationSeconds))}`
    : ''
  const resp = await fetch(`/api/custom/videos/${videoId}/poster${query}`, {
    method: 'PUT',
    headers: {
      'Content-Type': poster.blob.type || 'image/jpeg',
    },
    body: poster.blob,
  })
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({} as { error?: string }))
    throw new Error(data?.error || `上传封面失败（HTTP ${resp.status}）`)
  }
}

async function waitForVideoReady(
  videoId: string,
  cancel: UploadCancel,
  callbacks: UploadCallbacks,
): Promise<void> {
  const deadline = Date.now() + 10 * 60 * 1000
  while (Date.now() < deadline) {
    if (cancel.cancelled) throw new UploadCancelledError()
    const resp = await fetch(`/api/custom/videos/${videoId}`, { headers: { Accept: 'application/json' } })
    if (!resp.ok) throw new Error(`等待视频初始处理失败（HTTP ${resp.status}）`)
    const payload = await resp.json() as { data?: {
      status?: string
      thumbnail_url?: string
      duration_seconds?: number
      file_url?: string
      processing_error_summary?: string
    } }
    const video = payload.data
    if (video?.status === 'failed') {
      throw new Error(video.processing_error_summary || '视频初始处理失败')
    }
    if (
      ['ready', 'processing', 'completed'].includes(video?.status || '')
      && !!video?.thumbnail_url
      && Number(video?.duration_seconds) > 0
      && !!video?.file_url
    ) {
      callbacks.onProgress(99)
      return
    }
    callbacks.onProgress(98)
    await new Promise(resolve => window.setTimeout(resolve, 1500))
  }
  throw new Error('视频处理超时，暂未生成可用封面和元数据')
}

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.floor(seconds % 60)
  return minutes > 0 ? `${minutes}分${remainder}秒` : `${remainder}秒`
}

async function generateVideoPoster(file: File): Promise<GeneratedPoster> {
  if (typeof document === 'undefined') {
    throw new Error('browser-only')
  }
  return await new Promise<GeneratedPoster>((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const video = document.createElement('video')
    const cleanup = () => {
      URL.revokeObjectURL(url)
      video.removeAttribute('src')
      video.load()
    }
    const fail = (error: Error) => {
      cleanup()
      reject(error)
    }
    video.preload = 'metadata'
    video.muted = true
    video.playsInline = true
    video.src = url
    video.onloadedmetadata = () => {
      const seekTarget = Number.isFinite(video.duration) && video.duration > 0 ? Math.min(5, Math.max(0.1, video.duration - 0.1)) : 0
      try {
        video.currentTime = seekTarget
      } catch (error) {
        fail(error instanceof Error ? error : new Error('seek failed'))
      }
    }
    video.onerror = () => fail(new Error('load video metadata failed'))
    video.onseeked = () => {
      try {
        const canvas = document.createElement('canvas')
        canvas.width = video.videoWidth || 1280
        canvas.height = video.videoHeight || 720
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          fail(new Error('canvas context unavailable'))
          return
        }
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height)
        canvas.toBlob((blob) => {
          cleanup()
          if (!blob) {
            reject(new Error('create poster blob failed'))
            return
          }
          resolve({
            blob,
            durationSeconds: Number.isFinite(video.duration) && video.duration > 0
              ? Math.floor(video.duration)
              : 0,
          })
        }, 'image/jpeg', 0.9)
      } catch (error) {
        fail(error instanceof Error ? error : new Error('poster generation failed'))
      }
    }
  })
}

// runPool 简易并发池：最多 maxConcurrent 个 worker 同时消费 items
async function runPool<T>(
  items: T[],
  maxConcurrent: number,
  worker: (item: T) => Promise<void>,
): Promise<void> {
  let cursor = 0
  const workers = Array.from({ length: Math.min(maxConcurrent, items.length) }, async () => {
    while (cursor < items.length) {
      const idx = cursor++
      await worker(items[idx])
    }
  })
  await Promise.all(workers)
}

// putPartViaXhr 用 XHR PUT 单片到 MinIO，从响应头取 ETag
// fetch API 无法监听上传进度且无法读 ETag header（CORS 限制），必须用 XHR
async function putPartViaXhr(
  url: string,
  blob: Blob,
  cancel: UploadCancel,
  inFlightXhrs: XMLHttpRequest[],
  headers: Record<string, string>,
): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    inFlightXhrs.push(xhr)
    xhr.open('PUT', url)
    xhr.timeout = PART_TIMEOUT_MS
    Object.entries(headers).forEach(([key, value]) => xhr.setRequestHeader(key, value))

    // 取消信号：每 200ms 轮询 cancel.cancelled
    const poll = window.setInterval(() => {
      if (cancel.cancelled) {
        window.clearInterval(poll)
        xhr.abort()
      }
    }, 200)
    xhr.addEventListener('loadend', () => {
      window.clearInterval(poll)
      const idx = inFlightXhrs.indexOf(xhr)
      if (idx >= 0) inFlightXhrs.splice(idx, 1)
    })

    xhr.onload = () => {
      if (cancel.cancelled) {
        reject(new UploadCancelledError())
        return
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error(`分片 PUT 失败（HTTP ${xhr.status}）`))
        return
      }
      // MinIO 在响应头返回 ETag（分片 MD5），complete 时必需
      const etag = xhr.getResponseHeader('ETag') || ''
      if (!etag) {
        reject(new Error('分片上传成功但响应缺少 ETag 头'))
        return
      }
      resolve(etag.replace(/"/g, '')) // MinIO ETag 带双引号，去掉
    }

    xhr.onerror = () => reject(new Error('网络错误，分片上传失败，请检查生产服务连接'))
    xhr.ontimeout = () => reject(new Error(`分片上传超时（${Math.floor(PART_TIMEOUT_MS / 1000)}秒）`))
    xhr.onabort = () => {
      if (cancel.cancelled) reject(new UploadCancelledError())
      else reject(new Error('分片上传被中断'))
    }
    xhr.send(blob)
  })
}
