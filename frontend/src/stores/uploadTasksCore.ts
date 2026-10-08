import { computed, reactive, ref, watch, type WatchSource } from 'vue'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import {
  createListRefreshThrottle,
  createUploadQueue,
  defaultTimers,
  type Timers,
  type UploadBatch,
  type UploadPayload,
  type UploadQueueDeps,
} from './uploadQueue'
import { summarizeItems, type UploadItem } from './uploadTasksState'

/**
 * Everything the uploadTasks store does, with the API, page events and the
 * signed-in identity injected so it runs (and is tested) without the app.
 */

/**
 * Parallel transfers. The server hashes and stores every file before it
 * answers, so a small number keeps the pipe full without piling work on it.
 */
const CONCURRENCY = 3
const POLL_INTERVAL_MS = 3000
/** Keeps the ids=… query string of one status request well under URL length limits. */
const POLL_CHUNK_SIZE = 40
/** Minimum gap between mid-batch list refreshes pushed to an open page. */
const LIST_REFRESH_INTERVAL_MS = 2000
/** Batch ends this close together produce one final refresh. */
const LIST_REFRESH_COALESCE_MS = 150

export interface EnqueueUploadsInput {
  kbId: string
  kbName: string
  targetFolder?: string
  tagIds?: string[]
  processConfig?: KnowledgeProcessOverrides
  uploads: UploadPayload[]
}

export interface UploadTasksDeps {
  upload: UploadQueueDeps['upload']
  queryStatus: UploadQueueDeps['queryStatus']
  /** Ask an open knowledge base page or list to reload; `settled` marks a batch's last one. */
  emitListRefresh: (kbId: string, settled: boolean) => void
  /**
   * Who the uploads belong to, as getters of primitive ids. Changing any of
   * them drops the queue.
   */
  owner: WatchSource<unknown>[]
  timers?: Timers
}

export function createUploadTasks(deps: UploadTasksDeps) {
  const items = reactive<UploadItem[]>([])
  const batches = reactive<UploadBatch[]>([])
  const visible = ref(false)
  const collapsed = ref(false)
  const timers = deps.timers ?? defaultTimers

  const summary = computed(() => summarizeItems(items))
  const batchById = computed(() => new Map(batches.map(batch => [batch.id, batch])))

  const queue = createUploadQueue(items, batches, {
    concurrency: CONCURRENCY,
    pollIntervalMs: POLL_INTERVAL_MS,
    pollChunkSize: POLL_CHUNK_SIZE,
    upload: deps.upload,
    queryStatus: deps.queryStatus,
    onTransferEnd: createListRefreshThrottle(
      deps.emitListRefresh,
      LIST_REFRESH_INTERVAL_MS,
      LIST_REFRESH_COALESCE_MS,
      timers,
    ),
    timers,
  })

  const enqueue = (input: EnqueueUploadsInput) => {
    if (!input.kbId || input.uploads.length === 0) return
    // Batches that finished cleanly have nothing left to show; ones with
    // failures stay so their files can still be retried.
    queue.pruneFinished()
    queue.add({
      kbId: input.kbId,
      kbName: input.kbName,
      targetFolder: input.targetFolder || '',
      tagIds: input.tagIds && input.tagIds.length > 0 ? [...input.tagIds] : undefined,
      processConfig: input.processConfig,
    }, input.uploads)
    visible.value = true
    collapsed.value = false
  }

  /** Hide the panel and forget every task, cancelling transfers still sending. */
  const dismiss = () => {
    queue.clear()
    visible.value = false
  }

  const toggleCollapsed = () => {
    collapsed.value = !collapsed.value
  }

  // Uploads belong to the account and space they were started in. The ids are
  // separate watch sources on purpose: one getter returning a fresh array would
  // fire on every setUser(), even for the same person, and kill running uploads.
  watch(deps.owner, dismiss, { flush: 'sync' })

  return {
    items,
    batches,
    visible,
    collapsed,
    summary,
    batchById,
    enqueue,
    cancelItem: queue.cancel,
    cancelAll: queue.cancelAll,
    retryItem: queue.retry,
    retryFailed: queue.retryFailed,
    dismiss,
    toggleCollapsed,
  }
}
