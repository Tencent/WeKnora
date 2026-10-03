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
 * timers injected, so the store only wires it to the API and Vue.
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

export interface TransferEnd {
  /** The file reached the server, so the knowledge base has a new row. */
  uploaded: boolean
  /** Nothing is queued or uploading for this knowledge base any more. */
  settled: boolean
}

export interface Timers {
  setTimer: (fn: () => void, ms: number) => unknown
  clearTimer: (handle: unknown) => void
}

export const defaultTimers: Timers = {
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
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
  /** Called whenever a transfer ends, including one that outlived clear(). */
  onTransferEnd: (kbId: string, end: TransferEnd) => void
  timers?: Timers
}

/**
 * Consecutive successful polls a row must be missing from before it counts as
 * deleted, so one odd answer can't strand a live document as "Deleted".
 */
const MISSING_POLLS_BEFORE_DELETED = 3

let idSeq = 0
const nextId = (prefix: string) => `${prefix}-${Date.now().toString(36)}-${(idSeq++).toString(36)}`

export function createUploadQueue(items: UploadItem[], batches: UploadBatch[], deps: UploadQueueDeps) {
  const { setTimer, clearTimer } = deps.timers ?? defaultTimers

  // File payloads and abort handles stay out of the (possibly reactive) items:
  // proxying a File buys nothing and the panel never renders them.
  const payloads = new Map<string, UploadPayload>()
  const controllers = new Map<string, AbortController>()
  const missingPolls = new Map<string, number>()
  // Bumped by clear(), so work started before it cannot touch what comes after.
  let generation = 0
  let pollTimer: unknown = null

  const batchOf = (item: UploadItem) => batches.find(batch => batch.id === item.batchId)
  const findItem = (id: string) => items.find(item => item.id === id)

  const isBusy = (kbId: string) => items.some(item =>
    (item.transfer === 'queued' || item.transfer === 'uploading') && batchOf(item)?.kbId === kbId)

  // ---- transfer -----------------------------------------------------------

  // Identical files always have the same size. Keeping same-size files for one
  // knowledge base out of flight together lets the server's content-hash dedup,
  // which only sees rows already written, catch the second copy.
  const dedupKey = (item: UploadItem) => `${batchOf(item)?.kbId}:${item.size}`

  const pump = () => {
    for (const item of pickNextToStart(items, deps.concurrency, dedupKey)) void transfer(item)
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
    }
    // A retry after a cancel starts a new attempt under the same id; only the
    // latest attempt may write the item.
    const latestAttempt = controllers.get(item.id) === controller
    if (latestAttempt) controllers.delete(item.id)
    const aborted = controller.signal.aborted

    // Outlived clear(): the row is gone from the panel, but a file that still
    // made it (clear() lets fully sent files finish) is new in the list.
    if (startedIn !== generation) {
      if (!aborted && classifyUploadResult(result).kind === 'uploaded') {
        deps.onTransferEnd(batch.kbId, { uploaded: true, settled: !isBusy(batch.kbId) })
      }
      return
    }
    if (!latestAttempt) return

    if (aborted || item.transfer !== 'uploading') {
      // Cancelled mid-flight; the abort rejection carries nothing worth showing.
      if (item.transfer === 'uploading') item.transfer = 'cancelled'
    } else {
      const outcome = classifyUploadResult(result)
      if (outcome.kind === 'uploaded') {
        item.transfer = 'uploaded'
        item.loaded = item.size
        item.knowledgeId = outcome.knowledgeId
        item.parseStatus = outcome.parseStatus || 'pending'
        payloads.delete(item.id)
        schedulePoll()
      } else if (outcome.kind === 'duplicate') {
        item.transfer = 'duplicate'
        item.knowledgeId = outcome.knowledgeId
        payloads.delete(item.id)
      } else {
        item.transfer = 'failed'
        item.error = outcome.message
      }
    }
    deps.onTransferEnd(batch.kbId, { uploaded: item.transfer === 'uploaded', settled: !isBusy(batch.kbId) })
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
    const controller = controllers.get(id)
    if (controller) {
      // Its transfer reports the end once the abort lands.
      controller.abort()
      return
    }
    const kbId = batchOf(item)?.kbId
    if (kbId && !isBusy(kbId)) deps.onTransferEnd(kbId, { uploaded: false, settled: true })
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

  /** Drop batches whose every file is searchable; anything with an issue stays. */
  const pruneFinished = () => {
    for (const batch of [...batches]) {
      const own = items.filter(item => item.batchId === batch.id)
      if (!own.every(item => itemPhase(item) === 'ready')) continue
      for (const item of own) {
        items.splice(items.indexOf(item), 1)
        missingPolls.delete(item.id)
      }
      batches.splice(batches.indexOf(batch), 1)
    }
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
            if (!row) {
              const misses = (missingPolls.get(item.id) ?? 0) + 1
              missingPolls.set(item.id, misses)
              if (misses >= MISSING_POLLS_BEFORE_DELETED) item.parseStatus = 'deleted'
              continue
            }
            missingPolls.delete(item.id)
            item.parseStatus = row.parse_status
            item.parseError = row.error_message || undefined
          }
        }, () => {}))
      }
    }
    await Promise.all(requests)
    if (startedIn === generation) schedulePoll()
  }

  // ---- lifecycle ----------------------------------------------------------

  /**
   * Forget everything. Transfers still sending are aborted; ones whose body is
   * fully sent are left to finish, since the server stores them regardless.
   */
  const clear = () => {
    generation++
    for (const item of items) {
      if (itemPhase(item) !== 'saving') controllers.get(item.id)?.abort()
    }
    const busyKbs = new Set(items.filter(item => item.transfer === 'queued' || item.transfer === 'uploading')
      .map(item => batchOf(item)?.kbId)
      .filter((kbId): kbId is string => !!kbId))
    controllers.clear()
    payloads.clear()
    missingPolls.clear()
    items.splice(0)
    batches.splice(0)
    if (pollTimer !== null) {
      clearTimer(pollTimer)
      pollTimer = null
    }
    // Files that landed before the clear may still be waiting for a refresh.
    for (const kbId of busyKbs) deps.onTransferEnd(kbId, { uploaded: false, settled: true })
  }

  return { add, cancel, cancelAll, retry, retryFailed, pruneFinished, pollNow, clear }
}

/**
 * Turns transfer ends into `knowledgeFileUploaded` refreshes: while a knowledge
 * base still has work queued, at most one mid-batch (`settled: false`) refresh
 * per `intervalMs`; once it settles, a single final (`settled: true`) one.
 * Ends that settle within `coalesceMs` of each other (cancel-all aborting
 * several requests) produce one final refresh.
 */
export function createListRefreshThrottle(
  emit: (kbId: string, settled: boolean) => void,
  intervalMs: number,
  coalesceMs: number,
  timers: Timers = defaultTimers,
) {
  const throttles = new Map<string, unknown>()
  const pending = new Set<string>()
  const finals = new Map<string, unknown>()

  return (kbId: string, end: TransferEnd) => {
    if (end.settled) {
      const throttle = throttles.get(kbId)
      if (throttle !== undefined) timers.clearTimer(throttle)
      throttles.delete(kbId)
      pending.delete(kbId)
      if (finals.has(kbId)) return
      finals.set(kbId, timers.setTimer(() => {
        finals.delete(kbId)
        emit(kbId, true)
      }, coalesceMs))
      return
    }
    if (!end.uploaded) return
    if (throttles.has(kbId)) {
      pending.add(kbId)
      return
    }
    emit(kbId, false)
    const tick = () => {
      if (pending.delete(kbId)) {
        emit(kbId, false)
        throttles.set(kbId, timers.setTimer(tick, intervalMs))
      } else {
        throttles.delete(kbId)
      }
    }
    throttles.set(kbId, timers.setTimer(tick, intervalMs))
  }
}
