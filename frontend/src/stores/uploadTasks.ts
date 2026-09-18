import { computed, reactive, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { batchQueryKnowledge, uploadKnowledgeFile } from '@/api/knowledge-base/index'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import { useAuthStore } from './auth'
import { createUploadQueue, type KnowledgeStatusRow, type UploadBatch, type UploadPayload } from './uploadQueue'
import { summarizeItems, type UploadItem } from './uploadTasksState'

export type { UploadBatch } from './uploadQueue'

/**
 * Knowledge file uploads behind the floating progress panel
 * (components/upload-tasks/UploadTasksPanel.vue). Lives in a store rather than
 * the knowledge base page so uploads keep going, and stay visible, when the
 * user navigates elsewhere in the app. The queue mechanics are in uploadQueue.ts.
 */

/**
 * Parallel transfers. The server hashes and stores every file before it
 * answers, so a small number keeps the pipe full without piling work on it.
 */
const CONCURRENCY = 3
const POLL_INTERVAL_MS = 3000
/** Keeps the ids=… query string of one status request well under URL length limits. */
const POLL_CHUNK_SIZE = 40
/** Minimum gap between list refreshes pushed to an open knowledge base page. */
const LIST_REFRESH_INTERVAL_MS = 2000

export interface EnqueueUploadsInput {
  kbId: string
  kbName: string
  targetFolder?: string
  tagIds?: string[]
  processConfig?: KnowledgeProcessOverrides
  uploads: UploadPayload[]
}

export const useUploadTasksStore = defineStore('uploadTasks', () => {
  const items = reactive<UploadItem[]>([])
  const batches = reactive<UploadBatch[]>([])
  const visible = ref(false)
  const collapsed = ref(false)

  const summary = computed(() => summarizeItems(items))
  const batchById = computed(() => new Map(batches.map(batch => [batch.id, batch])))

  // Knowledge bases with transfers still queued or running, as a stable key.
  const busyKbKey = computed(() => {
    const kbIds = new Set<string>()
    for (const item of items) {
      if (item.transfer !== 'queued' && item.transfer !== 'uploading') continue
      const kbId = batchById.value.get(item.batchId)?.kbId
      if (kbId) kbIds.add(kbId)
    }
    return [...kbIds].sort().join('\n')
  })

  // Leading + trailing throttle per knowledge base, so an open knowledge base
  // page shows new rows as they land without reloading on every file.
  // `settled: false` marks a mid-batch refresh the page may skip; the batch's
  // last transfer always ends in a `settled: true` one.
  const refreshTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const refreshPending = new Set<string>()

  const emitUploaded = (kbId: string, settled: boolean) => {
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', { detail: { kbId, settled } }))
  }

  const requestListRefresh = (kbId: string) => {
    // The batch's last transfer: the settled refresh below covers it.
    if (!busyKbKey.value.split('\n').includes(kbId)) return
    if (refreshTimers.has(kbId)) {
      refreshPending.add(kbId)
      return
    }
    emitUploaded(kbId, false)
    const tick = () => {
      if (refreshPending.delete(kbId)) {
        emitUploaded(kbId, false)
        refreshTimers.set(kbId, setTimeout(tick, LIST_REFRESH_INTERVAL_MS))
      } else {
        refreshTimers.delete(kbId)
      }
    }
    refreshTimers.set(kbId, setTimeout(tick, LIST_REFRESH_INTERVAL_MS))
  }

  // A knowledge base's last transfer ended (uploaded, failed or cancelled):
  // replace any throttled refresh with the final one.
  watch(busyKbKey, (now, before) => {
    const stillBusy = new Set(now.split('\n'))
    for (const kbId of before.split('\n')) {
      if (!kbId || stillBusy.has(kbId)) continue
      clearTimeout(refreshTimers.get(kbId))
      refreshTimers.delete(kbId)
      refreshPending.delete(kbId)
      emitUploaded(kbId, true)
    }
  })

  const queue = createUploadQueue(items, batches, {
    concurrency: CONCURRENCY,
    pollIntervalMs: POLL_INTERVAL_MS,
    pollChunkSize: POLL_CHUNK_SIZE,
    upload: (batch, payload, onProgress, signal) => uploadKnowledgeFile(
      batch.kbId,
      {
        file: payload.file,
        fileName: payload.fileName,
        tag_ids: batch.tagIds,
        process_config: batch.processConfig,
      },
      (event: { loaded?: number; total?: number; progress?: number }) => {
        onProgress(event.progress ?? (event.total ? (event.loaded ?? 0) / event.total : 0))
      },
      { signal },
    ),
    queryStatus: async (kbId, knowledgeIds) => {
      const query = knowledgeIds.map(id => `ids=${encodeURIComponent(id)}`).join('&')
      try {
        const result: any = await batchQueryKnowledge(query, kbId)
        return result?.success && Array.isArray(result.data) ? (result.data as KnowledgeStatusRow[]) : null
      } catch {
        return null
      }
    },
    onUploaded: requestListRefresh,
  })

  const enqueue = (input: EnqueueUploadsInput) => {
    if (!input.kbId || input.uploads.length === 0) return
    // Starting over after everything settled: drop the finished run instead
    // of growing one endless list.
    if (summary.value.stage === 'done') queue.clear()
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

  /** Hide the panel and forget every task, cancelling transfers still running. */
  const dismiss = () => {
    queue.clear()
    visible.value = false
  }

  const toggleCollapsed = () => {
    collapsed.value = !collapsed.value
  }

  // Closing the tab kills in-flight requests; parsing is server side and survives.
  if (typeof window !== 'undefined') {
    window.addEventListener('beforeunload', event => {
      if (summary.value.stage !== 'uploading') return
      event.preventDefault()
      event.returnValue = ''
    })
  }

  // Uploads belong to the account and space they were started in.
  const auth = useAuthStore()
  watch(() => [auth.user?.id, auth.effectiveTenantId], dismiss, { flush: 'sync' })

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
})
