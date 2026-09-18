import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import {
  classifyUploadResult,
  itemPhase,
  needsParsePolling,
  pickNextToStart,
  type UploadItem,
} from './uploadTasksState'

/**
 * The upload queue's behaviour — bounded concurrent transfers, cancel, retry
 * and polling the created rows until parsing settles — with the network and
 * timers injected, so stores/uploadTasks.ts only wires it to the API and Vue.
 *
 * `items` and `batches` are mutated in place (never reassigned) so a reactive
 * array handed in by the store stays the one the panel renders.
 */

export interface UploadBatch {
  id: string
  kbId: string
  kbName: string
  /** Destination folder, '' for the knowledge base root. */
  targetFolder: string
  tagIds?: string[]
  processConfig?: KnowledgeProcessOverrides
}

export interface UploadPayload {
  file: File
  /** Path-qualified name the backend splits into folder + file name. */
  fileName?: string
}

export interface KnowledgeStatusRow {
  id: string
  parse_status?: string
  error_message?: string
}

export interface UploadQueueDeps {
  concurrency: number
  pollIntervalMs: number
  /** Most knowledge ids asked about in one status request. */
  pollChunkSize: number
  /** Resolves or rejects with the API's response body; the queue classifies either. */
  upload: (
    batch: UploadBatch,
    payload: UploadPayload,
    onProgress: (ratio: number) => void,
    signal: AbortSignal,
  ) => Promise<unknown>
  /** Current rows for the ids, or null when the request failed. */
  queryStatus: (kbId: string, knowledgeIds: string[]) => Promise<KnowledgeStatusRow[] | null>
  /** A file reached the server, so its knowledge base's list has a new row. */
  onUploaded: (kbId: string) => void
  setTimer?: (fn: () => void, ms: number) => unknown
  clearTimer?: (handle: unknown) => void
}

let idSeq = 0
const nextId = (prefix: string) => `${prefix}-${Date.now().toString(36)}-${(idSeq++).toString(36)}`

export function createUploadQueue(items: UploadItem[], batches: UploadBatch[], deps: UploadQueueDeps) {
  const setTimer = deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms))
  const clearTimer = deps.clearTimer ?? (handle => clearTimeout(handle as ReturnType<typeof setTimeout>))

  // File payloads and abort handles stay out of the (possibly reactive) items:
  // proxying a File buys nothing and the panel never renders them.
  const payloads = new Map<string, UploadPayload>()
  const controllers = new Map<string, AbortController>()
  // Bumped by clear(), so work started before it cannot touch what comes after.
  let generation = 0
  let pollTimer: unknown = null

  const batchOf = (item: UploadItem) => batches.find(batch => batch.id === item.batchId)
  const findItem = (id: string) => items.find(item => item.id === id)

  // ---- transfer -----------------------------------------------------------

  const pump = () => {
    for (const item of pickNextToStart(items, deps.concurrency)) void transfer(item)
  }

  const transfer = async (item: UploadItem) => {
    const batch = batchOf(item)
    const payload = payloads.get(item.id)
    if (!batch || !payload) {
      item.transfer = 'failed'
      return
    }
    const startedIn = generation
    const controller = new AbortController()
    controllers.set(item.id, controller)
    item.transfer = 'uploading'
    item.loaded = 0
    item.error = undefined

    let result: unknown
    try {
      result = await deps.upload(batch, payload, ratio => {
        if (item.transfer === 'uploading') item.loaded = Math.min(item.size, Math.round(ratio * item.size))
      }, controller.signal)
    } catch (error) {
      result = error
    } finally {
      controllers.delete(item.id)
    }

    if (startedIn !== generation) return
    // Cancelled mid-flight: the abort rejection carries nothing worth showing.
    if (item.transfer !== 'uploading') {
      pump()
      return
    }

    const outcome = classifyUploadResult(result)
    if (outcome.kind === 'uploaded') {
      item.transfer = 'uploaded'
      item.loaded = item.size
      item.knowledgeId = outcome.knowledgeId
      item.parseStatus = outcome.parseStatus || 'pending'
      payloads.delete(item.id)
      deps.onUploaded(batch.kbId)
      schedulePoll()
    } else if (outcome.kind === 'duplicate') {
      item.transfer = 'duplicate'
      item.knowledgeId = outcome.knowledgeId
      payloads.delete(item.id)
    } else {
      item.transfer = 'failed'
      item.error = outcome.message
    }
    pump()
  }

  const add = (batchInput: Omit<UploadBatch, 'id'>, uploads: UploadPayload[]) => {
    const batch: UploadBatch = { ...batchInput, id: nextId('batch') }
    batches.push(batch)
    for (const upload of uploads) {
      const id = nextId('upload')
      payloads.set(id, upload)
      items.push({
        id,
        batchId: batch.id,
        name: upload.file.name,
        relativePath: upload.file.webkitRelativePath || '',
        size: upload.file.size,
        loaded: 0,
        transfer: 'queued',
      })
    }
    pump()
    return batch
  }

  const cancel = (id: string) => {
    const item = findItem(id)
    if (!item || (item.transfer !== 'queued' && item.transfer !== 'uploading')) return
    // Once the whole body is sent the server is already storing the file;
    // aborting now would hide a row that still gets created.
    if (itemPhase(item) === 'saving') return
    item.transfer = 'cancelled'
    item.loaded = 0
    controllers.get(id)?.abort()
  }

  const cancelAll = () => {
    for (const item of items) cancel(item.id)
  }

  const retry = (id: string) => {
    const item = findItem(id)
    if (!item || (item.transfer !== 'failed' && item.transfer !== 'cancelled') || !payloads.has(id)) return
    item.transfer = 'queued'
    item.loaded = 0
    item.error = undefined
    pump()
  }

  const retryFailed = () => {
    for (const item of items) retry(item.id)
  }

  // ---- parse polling ------------------------------------------------------

  function schedulePoll() {
    if (pollTimer !== null || !items.some(needsParsePolling)) return
    pollTimer = setTimer(() => {
      pollTimer = null
      void pollNow()
    }, deps.pollIntervalMs)
  }

  const pollNow = async () => {
    const startedIn = generation
    const byKb = new Map<string, UploadItem[]>()
    for (const item of items) {
      const kbId = needsParsePolling(item) ? batchOf(item)?.kbId : undefined
      if (!kbId) continue
      if (!byKb.has(kbId)) byKb.set(kbId, [])
      byKb.get(kbId)!.push(item)
    }

    const requests: Promise<void>[] = []
    for (const [kbId, group] of byKb) {
      for (let i = 0; i < group.length; i += deps.pollChunkSize) {
        const chunk = group.slice(i, i + deps.pollChunkSize)
        requests.push(deps.queryStatus(kbId, chunk.map(item => item.knowledgeId!)).then(rows => {
          // A failed request just waits for the next tick.
          if (!rows || startedIn !== generation) return
          const byId = new Map(rows.map(row => [row.id, row]))
          for (const item of chunk) {
            const row = byId.get(item.knowledgeId!)
            // Absent from a successful answer: deleted while we were watching.
            item.parseStatus = row ? row.parse_status : 'deleted'
            item.parseError = row?.error_message || undefined
          }
        }, () => {}))
      }
    }
    await Promise.all(requests)
    if (startedIn === generation) schedulePoll()
  }

  // ---- lifecycle ----------------------------------------------------------

  /** Forget everything, aborting transfers still running. */
  const clear = () => {
    generation++
    for (const controller of controllers.values()) controller.abort()
    controllers.clear()
    payloads.clear()
    items.splice(0)
    batches.splice(0)
    if (pollTimer !== null) {
      clearTimer(pollTimer)
      pollTimer = null
    }
  }

  return { add, cancel, cancelAll, retry, retryFailed, pollNow, clear }
}
