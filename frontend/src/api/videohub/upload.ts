import type { UploadForm, VideoData } from '@/types/videohub'

export interface UploadMetrics {
  totalBytes: number
  totalDurationMs: number
  throughputBytesPerSecond: number
  partCount: number
  completedParts: number
  retryCount: number
  retryRate: number
  averagePartDurationMs: number
  finalConcurrency: number
  directUpload: boolean
}

export interface UploadCallbacks {
  onProgress(percent: number): void
  onMetrics?(metrics: UploadMetrics): void
}
export interface UploadCancel { cancelled: boolean }
export interface MultipartCompletePart {
  part_number: number
  etag: string
}
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

// 服务端会按文件大小返回 8MB 或 16MB；该值只作为兼容旧后端的兜底。
export const PART_SIZE = 8 * 1024 * 1024
export const DEFAULT_INITIAL_CONCURRENCY = 2
export const DEFAULT_MIN_CONCURRENCY = 1
export const DEFAULT_MAX_CONCURRENCY = 4
// 单片最大重试次数：网络抖动时重传，避免整文件从头再来
export const MAX_RETRIES = 3
const RETRY_BACKOFF_MS = 700
const PART_TIMEOUT_MS = 5 * 60 * 1000
const UPLOAD_TRACE_HEADER = 'X-Upload-Trace-ID'
const UPLOAD_ATTEMPT_HEADER = 'X-Upload-Attempt'
const MAX_LOG_RESPONSE_BODY_LENGTH = 2000

interface UploadTrace {
  traceId: string
}

interface UploadRequestResult {
  response: Response
  responseBody: string
}

interface MultipartInitResponse {
  video_id: string
  object_key: string
  upload_id: string
  part_size_bytes?: number
  recommended_part_size_bytes?: number
  initial_concurrency?: number
  min_concurrency?: number
  max_concurrency?: number
  sign_ttl_seconds?: number
  direct_upload?: boolean
  already_exists?: boolean
}

interface MultipartSignResponse {
  part_url: string
  expires_at?: string
  direct_upload?: boolean
}

// 分片直传架构（VP-T002）：
// 1) POST /api/custom/uploads/multipart/init 拿 upload_id + video_id + object_key
// 2) 后端签名，浏览器优先 PUT 到公网 MinIO；未暴露 MinIO 时走受校验网关
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
  const trace: UploadTrace = { traceId: createUploadTraceId() }

  // 提到外层以便 catch 调 abort 清理
  let init: MultipartInitResponse | undefined
  let multipartCompleted = false
  const uploadStartedAt = Date.now()

  logUploadEvent('upload_start', trace, {
    filename: file.name,
    file_size: file.size,
    part_size: PART_SIZE,
    max_concurrency: DEFAULT_MAX_CONCURRENCY,
    max_retries: MAX_RETRIES,
  })
  callbacks.onProgress(2)

  return (async () => {
    // 步骤 1：init —— 后端创建 multipart upload + 占位 video 记录
    const initRequest = await fetchWithUploadDiagnostics(
      '/api/custom/uploads/multipart/init',
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          [UPLOAD_TRACE_HEADER]: trace.traceId,
        },
        body: JSON.stringify({
          filename: file.name,
          content_type: file.type || 'application/octet-stream',
          video_type: videoType,
          file_size_bytes: file.size,
          part_size_bytes: 0,
          idempotency_key: trace.traceId,
        }),
      },
      trace,
      { stage: 'init' },
    )
    const initRes = initRequest.response
    if (cancel.cancelled) throw new UploadCancelledError()
    if (!initRes.ok) {
      const data = parseJson<{ error?: string }>(initRequest.responseBody)
      throw new Error(data?.error || `初始化分片上传失败（HTTP ${initRes.status}）`)
    }
    init = parseJson<MultipartInitResponse>(initRequest.responseBody)
    if (!init?.video_id || !init.object_key || !init.upload_id) {
      throw new Error('初始化分片上传响应缺少 video_id、object_key 或 upload_id')
    }
    const partSize = init.part_size_bytes || init.recommended_part_size_bytes || PART_SIZE
    const initialConcurrency = init.initial_concurrency || DEFAULT_INITIAL_CONCURRENCY
    const minConcurrency = init.min_concurrency || DEFAULT_MIN_CONCURRENCY
    const maxConcurrency = Math.max(minConcurrency, init.max_concurrency || DEFAULT_MAX_CONCURRENCY)
    const directUpload = !!init.direct_upload
    logUploadEvent('init_ready', trace, {
      video_id: init.video_id,
      upload_id: init.upload_id,
      object_key: init.object_key,
      part_size: partSize,
      initial_concurrency: initialConcurrency,
      min_concurrency: minConcurrency,
      max_concurrency: maxConcurrency,
      direct_upload: directUpload,
    })

    // 步骤 2：切片 + 并发上传
    const totalParts = getMultipartPartSizes(file.size, partSize).length
    const uploadedBytes = { count: 0 }
    const completedParts = new Map<number, string>()
    const partDurations: number[] = []
    let retryCount = 0
    const concurrency = new AdaptiveConcurrencyController({
      initial: initialConcurrency,
      min: minConcurrency,
      max: maxConcurrency,
    })
    // 跟踪 in-flight XHR，用户取消时统一 abort
    const inFlightXhrs: XMLHttpRequest[] = []

    const partIndexes = Array.from({ length: totalParts }, (_, i) => i)
    await runAdaptivePool(partIndexes, concurrency, async (partIdx) => {
      if (cancel.cancelled) throw new UploadCancelledError()

      const partNumber = partIdx + 1
      const start = partIdx * partSize
      const end = Math.min(start + partSize, file.size)
      const blob = file.slice(start, end)
      const partStartedAt = Date.now()
      let attempts = 0

      const etag = await uploadPartWithRetry(
        partNumber,
        totalParts,
        async (attempt) => {
          attempts = attempt
          const signed = await signMultipartPart(init!, partNumber, trace, attempt)
          const direct = signed.direct_upload ?? directUpload
          const headers: Record<string, string> = direct
            ? {}
            : {
                'X-Video-ID': init!.video_id,
                'X-Object-Key': init!.object_key,
                'X-Upload-ID': init!.upload_id,
                'X-Part-Number': String(partNumber),
                [UPLOAD_TRACE_HEADER]: trace.traceId,
                [UPLOAD_ATTEMPT_HEADER]: String(attempt),
              }
          return putPartViaXhr(
            signed.part_url || '/api/custom/uploads/multipart/part',
            blob,
            cancel,
            inFlightXhrs,
            headers,
            trace,
            { videoId: init!.video_id, uploadId: init!.upload_id, partNumber, attempt, totalParts, directUpload: direct },
          )
        },
        {
          cancel,
          maxRetries: MAX_RETRIES,
          retryDelayMs: RETRY_BACKOFF_MS,
          onAttemptFailed: (attempt, error) => {
            retryCount++
            concurrency.recordFailure()
            logUploadEvent('part_attempt_failed', trace, {
              video_id: init!.video_id,
              upload_id: init!.upload_id,
              part_number: partNumber,
              attempt,
              max_retries: MAX_RETRIES,
              current_concurrency: concurrency.current,
              error: errorMessage(error),
            }, 'warn')
          },
          onRetryExhausted: (error) => logUploadEvent('part_retry_exhausted', trace, {
            video_id: init!.video_id,
            upload_id: init!.upload_id,
            part_number: partNumber,
            attempts: MAX_RETRIES,
            error: errorMessage(error),
          }, 'error'),
        },
      )
      const elapsedMs = Date.now() - partStartedAt
      partDurations.push(elapsedMs)
      concurrency.recordSuccess(elapsedMs, attempts > 1)
      if (completedParts.has(partNumber)) {
        throw new Error(`分片 ${partNumber}/${totalParts} 重复上传完成`)
      }
      completedParts.set(partNumber, etag)
      uploadedBytes.count += (end - start)
      // 进度映射：2% → 95%（按已上传字节比例推进，每片完成立即触发）
      const pct = 2 + Math.floor((uploadedBytes.count / file.size) * 93)
      callbacks.onProgress(Math.min(pct, 95))
    })

    const totalDurationMs = Math.max(1, Date.now() - uploadStartedAt)
    const metrics: UploadMetrics = {
      totalBytes: file.size,
      totalDurationMs,
      throughputBytesPerSecond: file.size / (totalDurationMs / 1000),
      partCount: totalParts,
      completedParts: completedParts.size,
      retryCount,
      retryRate: retryCount / Math.max(1, retryCount + completedParts.size),
      averagePartDurationMs: partDurations.reduce((sum, value) => sum + value, 0) / Math.max(1, partDurations.length),
      finalConcurrency: concurrency.current,
      directUpload,
    }
    callbacks.onMetrics?.(metrics)
    logUploadEvent('upload_metrics', trace, { ...metrics })

    if (cancel.cancelled) throw new UploadCancelledError()

    // 步骤 3：complete —— 后端合并分片 + 入初始处理任务
    callbacks.onProgress(97)
    const completeRequest = await fetchWithUploadDiagnostics(
      '/api/custom/uploads/multipart/complete',
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          [UPLOAD_TRACE_HEADER]: trace.traceId,
        },
        body: JSON.stringify({
          video_id: init!.video_id,
          object_key: init!.object_key,
          upload_id: init!.upload_id,
          parts: buildMultipartCompleteParts(completedParts, totalParts),
        }),
      },
      trace,
      {
        stage: 'complete',
        videoId: init!.video_id,
        uploadId: init!.upload_id,
      },
    )
    const completeRes = completeRequest.response
    if (cancel.cancelled) throw new UploadCancelledError()
    if (!completeRes.ok) {
      const data = parseJson<{ error?: string }>(completeRequest.responseBody)
      throw new Error('合并分片失败：' + (data?.error || `HTTP ${completeRes.status}`))
    }
    multipartCompleted = true
    const completed = parseJson<{ video_id: string; uploaded_at: string }>(completeRequest.responseBody)
    logUploadEvent('complete_ready', trace, {
      video_id: init!.video_id,
      upload_id: init!.upload_id,
      job_id: (completed as { job_id?: string }).job_id,
    })

    const poster = await generateVideoPoster(file).catch((error) => {
      logUploadEvent('poster_generate_failed', trace, { error: errorMessage(error) }, 'warn')
      return null
    })
    if (poster) {
      await uploadVideoPoster(completed.video_id, poster, trace).catch((error) => {
        logUploadEvent('poster_upload_failed', trace, {
          video_id: completed.video_id,
          error: errorMessage(error),
        }, 'warn')
      })
    }

    await waitForVideoReady(completed.video_id, cancel, callbacks, trace)
    callbacks.onProgress(100)

    // UploadModal 上传成功后调 afterUpload 刷新列表，video_url 由列表/详情接口补全
    return {
      id: completed.video_id,
      title: file.name.replace(/\.[^.]+$/, ''),
      category: 'training',
      categoryName: '培训',
      duration: formatDuration(poster?.durationSeconds || 0),
      durationSeconds: Math.floor(poster?.durationSeconds || 0),
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
      const failureReason = err instanceof UploadCancelledError
        ? 'browser_cancelled'
        : 'upload_failed'
      logUploadEvent('abort_start', trace, {
        video_id: init.video_id,
        upload_id: init.upload_id,
        reason: failureReason,
        error: errorMessage(err),
      }, 'warn')
      await fetchWithUploadDiagnostics(
        '/api/custom/uploads/multipart/abort',
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            [UPLOAD_TRACE_HEADER]: trace.traceId,
          },
          body: JSON.stringify({
            video_id: init.video_id,
            object_key: init.object_key,
            upload_id: init.upload_id,
            reason: `${failureReason}: ${errorMessage(err)}`,
          }),
        },
        trace,
        {
          stage: 'abort',
          videoId: init.video_id,
          uploadId: init.upload_id,
        },
      ).catch((abortError) => {
        logUploadEvent('abort_failed', trace, {
          video_id: init?.video_id,
          upload_id: init?.upload_id,
          error: errorMessage(abortError),
        }, 'error')
      })
    }
    logUploadEvent('upload_failed', trace, { error: errorMessage(err) }, 'error')
    throw err
  }) as Promise<VideoData>
}

export function buildMultipartCompleteParts(
  completedParts: Map<number, string> | MultipartCompletePart[],
  totalParts: number,
): MultipartCompletePart[] {
  if (!Number.isInteger(totalParts) || totalParts <= 0) {
    throw new Error('分片总数无效')
  }

  const byNumber = completedParts instanceof Map
    ? new Map(completedParts)
    : completedParts.reduce((acc, part) => {
        if (acc.has(part.part_number)) {
          throw new Error(`分片 ${part.part_number} 重复上传完成`)
        }
        acc.set(part.part_number, part.etag)
        return acc
      }, new Map<number, string>())

  for (const partNumber of byNumber.keys()) {
    if (!Number.isInteger(partNumber) || partNumber < 1 || partNumber > totalParts) {
      throw new Error(`分片编号无效：${partNumber}`)
    }
  }

  if (byNumber.size !== totalParts) {
    throw new Error(`分片上传未完成：已完成 ${byNumber.size}/${totalParts}`)
  }

  return Array.from({ length: totalParts }, (_, idx) => {
    const partNumber = idx + 1
    const etag = (byNumber.get(partNumber) || '').trim().replace(/^"+|"+$/g, '')
    if (!etag) {
      throw new Error(`分片 ${partNumber}/${totalParts} 缺少 ETag`)
    }
    return { part_number: partNumber, etag }
  })
}

export function getMultipartPartSizes(
  fileSize: number,
  partSize = PART_SIZE,
): number[] {
  if (!Number.isSafeInteger(fileSize) || fileSize <= 0) {
    throw new Error('文件大小无效')
  }
  if (!Number.isSafeInteger(partSize) || partSize <= 0) {
    throw new Error('分片大小无效')
  }
  const totalParts = Math.ceil(fileSize / partSize)
  return Array.from({ length: totalParts }, (_, index) => {
    const start = index * partSize
    return Math.min(partSize, fileSize - start)
  })
}

export interface AdaptiveConcurrencyOptions {
  initial: number
  min: number
  max: number
  targetPartDurationMs?: number
  stableSuccessesToIncrease?: number
}

export class AdaptiveConcurrencyController {
  private readonly minimum: number
  private readonly maximum: number
  private readonly targetPartDurationMs: number
  private readonly stableSuccessesToIncrease: number
  private stableSuccesses = 0
  private value: number

  constructor(options: AdaptiveConcurrencyOptions) {
    this.minimum = Math.max(1, Math.floor(options.min || 1))
    this.maximum = Math.max(this.minimum, Math.floor(options.max || this.minimum))
    this.value = Math.min(this.maximum, Math.max(this.minimum, Math.floor(options.initial || this.minimum)))
    this.targetPartDurationMs = options.targetPartDurationMs ?? 60_000
    this.stableSuccessesToIncrease = Math.max(1, options.stableSuccessesToIncrease ?? 2)
  }

  get current(): number {
    return this.value
  }

  recordFailure(): void {
    this.stableSuccesses = 0
    this.value = Math.max(this.minimum, this.value - 1)
  }

  recordSuccess(durationMs: number, retried: boolean): void {
    if (retried || durationMs > this.targetPartDurationMs) {
      this.stableSuccesses = 0
      return
    }
    if (this.value >= this.maximum) return
    this.stableSuccesses++
    if (this.stableSuccesses >= this.stableSuccessesToIncrease) {
      this.value++
      this.stableSuccesses = 0
    }
  }
}

export async function runAdaptivePool<T>(
  items: T[],
  controller: AdaptiveConcurrencyController,
  worker: (item: T) => Promise<void>,
): Promise<void> {
  if (items.length === 0) return
  await new Promise<void>((resolve, reject) => {
    let cursor = 0
    let active = 0
    let settled = false

    const pump = () => {
      if (settled) return
      while (active < controller.current && cursor < items.length) {
        const item = items[cursor++]
        active++
        Promise.resolve(worker(item)).then(() => {
          active--
          if (cursor >= items.length && active === 0) {
            settled = true
            resolve()
            return
          }
          pump()
        }).catch(error => {
          if (settled) return
          settled = true
          reject(error)
        })
      }
    }

    pump()
  })
}

export interface MultipartRetryOptions {
  cancel?: UploadCancel
  maxRetries?: number
  retryDelayMs?: number
  sleep?: (ms: number) => Promise<void>
  onAttemptFailed?: (attempt: number, error: unknown) => void
  onRetryExhausted?: (error: unknown) => void
}

async function signMultipartPart(
  init: MultipartInitResponse,
  partNumber: number,
  trace: UploadTrace,
  attempt: number,
): Promise<MultipartSignResponse> {
  const request = await fetchWithUploadDiagnostics(
    '/api/custom/uploads/multipart/sign',
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [UPLOAD_TRACE_HEADER]: trace.traceId,
        [UPLOAD_ATTEMPT_HEADER]: String(attempt),
      },
      body: JSON.stringify({
        video_id: init.video_id,
        object_key: init.object_key,
        upload_id: init.upload_id,
        part_number: partNumber,
      }),
    },
    trace,
    { stage: 'sign', videoId: init.video_id, uploadId: init.upload_id, partNumber, attempt },
  )
  if (!request.response.ok) {
    const data = parseJson<{ error?: string }>(request.responseBody)
    throw new Error(data?.error || `分片签名失败（HTTP ${request.response.status}）`)
  }
  const signed = parseJson<MultipartSignResponse>(request.responseBody)
  if (!signed.part_url) throw new Error('分片签名响应缺少 part_url')
  return signed
}

// Keep retry behavior independent from XHR so the browser path and the
// large-file regression tests exercise the same state machine.
export async function uploadPartWithRetry<T>(
  partNumber: number,
  totalParts: number,
  uploadAttempt: (attempt: number) => Promise<T>,
  options: MultipartRetryOptions = {},
): Promise<T> {
  const maxRetries = options.maxRetries ?? MAX_RETRIES
  const retryDelayMs = options.retryDelayMs ?? RETRY_BACKOFF_MS
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms)))
  let lastError: unknown = new Error('未知错误')

  for (let attempt = 1; attempt <= maxRetries; attempt++) {
    if (options.cancel?.cancelled) throw new UploadCancelledError()
    try {
      return await uploadAttempt(attempt)
    } catch (error) {
      if (error instanceof UploadCancelledError) throw error
      lastError = error
      options.onAttemptFailed?.(attempt, error)
      if (attempt < maxRetries) await sleep(retryDelayMs)
    }
  }

  options.onRetryExhausted?.(lastError)
  throw new Error(`分片 ${partNumber}/${totalParts} 上传失败：${errorMessage(lastError)}`)
}

async function uploadVideoPoster(videoId: string, poster: GeneratedPoster, trace: UploadTrace): Promise<void> {
  const query = poster.durationSeconds > 0
    ? `?duration_seconds=${encodeURIComponent(Math.floor(poster.durationSeconds))}`
    : ''
  const request = await fetchWithUploadDiagnostics(
    `/api/custom/videos/${videoId}/poster${query}`,
    {
      method: 'PUT',
      headers: {
        'Content-Type': poster.blob.type || 'image/jpeg',
        [UPLOAD_TRACE_HEADER]: trace.traceId,
      },
      body: poster.blob,
    },
    trace,
    { stage: 'poster', videoId },
  )
  const resp = request.response
  if (!resp.ok) {
    const data = parseJson<{ error?: string }>(request.responseBody)
    throw new Error(data?.error || `上传封面失败（HTTP ${resp.status}）`)
  }
}

async function waitForVideoReady(
  videoId: string,
  cancel: UploadCancel,
  callbacks: UploadCallbacks,
  trace: UploadTrace,
): Promise<void> {
  const deadline = Date.now() + 10 * 60 * 1000
  while (Date.now() < deadline) {
    if (cancel.cancelled) throw new UploadCancelledError()
    const request = await fetchWithUploadDiagnostics(
      `/api/custom/videos/${videoId}`,
      {
        headers: {
          Accept: 'application/json',
          [UPLOAD_TRACE_HEADER]: trace.traceId,
        },
      },
      trace,
      { stage: 'ready_poll', videoId },
    )
    const resp = request.response
    if (!resp.ok) throw new Error(`等待视频初始处理失败（HTTP ${resp.status}）`)
    const payload = parseJson<{ data?: {
      status?: string
      thumbnail_url?: string
      duration_seconds?: number
      file_url?: string
      processing_error_summary?: string
    } }>(request.responseBody)
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
  trace: UploadTrace,
  metadata: { videoId: string; uploadId: string; partNumber: number; attempt: number; totalParts: number; directUpload?: boolean },
): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const startedAt = Date.now()
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
        logUploadEvent('part_cancelled_after_response', trace, {
          ...metadata,
          http_status: xhr.status,
          elapsed_ms: Date.now() - startedAt,
          response_body: truncateForLog(xhr.responseText),
        }, 'warn')
        reject(new UploadCancelledError())
        return
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        const detail = responseDetail(xhr.status, xhr.responseText)
        logUploadEvent('part_response_failed', trace, {
          ...metadata,
          http_status: xhr.status,
          elapsed_ms: Date.now() - startedAt,
          response_body: truncateForLog(xhr.responseText),
          error: detail,
        }, 'error')
        reject(new Error(`分片 ${metadata.partNumber}/${metadata.totalParts} PUT 失败（HTTP ${xhr.status}）：${detail}`))
        return
      }
      // MinIO 在响应头返回 ETag（分片 MD5），complete 时必需
      const etag = xhr.getResponseHeader('ETag') || ''
      if (!etag) {
        const detail = '分片上传成功但响应缺少 ETag 头'
        logUploadEvent('part_missing_etag', trace, {
          ...metadata,
          http_status: xhr.status,
          elapsed_ms: Date.now() - startedAt,
          response_body: truncateForLog(xhr.responseText),
          error: detail,
        }, 'error')
        reject(new Error(detail))
        return
      }
      logUploadEvent('part_succeeded', trace, {
        ...metadata,
        http_status: xhr.status,
        elapsed_ms: Date.now() - startedAt,
        response_body: truncateForLog(xhr.responseText),
        etag,
      })
      resolve(etag.replace(/"/g, '')) // MinIO ETag 带双引号，去掉
    }

    xhr.onerror = () => {
      const detail = '连接中断（XHR 状态 0，可能是 Vite/Nginx 代理断开或浏览器取消）'
      logUploadEvent('part_xhr_error', trace, {
        ...metadata,
        http_status: xhr.status || 0,
        elapsed_ms: Date.now() - startedAt,
        response_body: truncateForLog(xhr.responseText),
        error: detail,
      }, 'error')
      reject(new Error(`分片 ${metadata.partNumber}/${metadata.totalParts} ${detail}`))
    }
    xhr.ontimeout = () => {
      const detail = `分片请求超时（${Math.floor(PART_TIMEOUT_MS / 1000)} 秒）`
      logUploadEvent('part_xhr_timeout', trace, {
        ...metadata,
        http_status: xhr.status || 0,
        elapsed_ms: Date.now() - startedAt,
        response_body: truncateForLog(xhr.responseText),
        error: detail,
      }, 'error')
      reject(new Error(`分片 ${metadata.partNumber}/${metadata.totalParts} ${detail}`))
    }
    xhr.onabort = () => {
      const detail = cancel.cancelled ? 'browser_cancelled' : 'xhr_aborted'
      logUploadEvent('part_xhr_abort', trace, {
        ...metadata,
        http_status: xhr.status || 0,
        elapsed_ms: Date.now() - startedAt,
        response_body: truncateForLog(xhr.responseText),
        error: detail,
      }, 'warn')
      if (cancel.cancelled) reject(new UploadCancelledError())
      else reject(new Error(`分片 ${metadata.partNumber}/${metadata.totalParts} 连接被中断`))
    }
    xhr.send(blob)
  })
}

function createUploadTraceId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `upload-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function fetchWithUploadDiagnostics(
  input: RequestInfo | URL,
  init: RequestInit,
  trace: UploadTrace,
  metadata: Record<string, unknown>,
): Promise<UploadRequestResult> {
  const startedAt = Date.now()
  try {
    const response = await fetch(input, init)
    const responseBody = await response.text()
    const fields = {
      ...metadata,
      http_status: response.status,
      elapsed_ms: Date.now() - startedAt,
      response_body: truncateForLog(responseBody),
    }
    logUploadEvent(response.ok ? 'request_succeeded' : 'request_failed', trace, fields, response.ok ? 'info' : 'error')
    return { response, responseBody }
  } catch (error) {
    logUploadEvent('request_network_error', trace, {
      ...metadata,
      http_status: 0,
      elapsed_ms: Date.now() - startedAt,
      error: errorMessage(error),
    }, 'error')
    throw error
  }
}

function parseJson<T>(body: string): T {
  try {
    return JSON.parse(body) as T
  } catch {
    return {} as T
  }
}

function responseDetail(status: number, body: string): string {
  const parsed = parseJson<{ error?: string }>(body)
  return parsed.error || truncateForLog(body) || `HTTP ${status}`
}

function truncateForLog(value: string): string {
  if (!value) return ''
  return value.length > MAX_LOG_RESPONSE_BODY_LENGTH
    ? `${value.slice(0, MAX_LOG_RESPONSE_BODY_LENGTH)}...`
    : value
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function logUploadEvent(
  event: string,
  trace: UploadTrace,
  fields: Record<string, unknown> = {},
  level: 'info' | 'warn' | 'error' = 'info',
): void {
  const payload = {
    component: 'video-upload',
    event,
    trace_id: trace.traceId,
    ...fields,
  }
  if (level === 'error') {
    console.error('[video-upload]', payload)
  } else if (level === 'warn') {
    console.warn('[video-upload]', payload)
  } else {
    console.info('[video-upload]', payload)
  }
}
