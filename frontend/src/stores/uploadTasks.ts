import { defineStore } from 'pinia'
import { batchQueryKnowledge, uploadKnowledgeFile } from '@/api/knowledge-base/index'
import { useAuthStore } from './auth'
import type { KnowledgeStatusRow } from './uploadQueue'
import { createUploadTasks } from './uploadTasksCore'

export type { UploadBatch } from './uploadQueue'
export type { EnqueueUploadsInput } from './uploadTasksCore'

/**
 * Knowledge file uploads behind the floating progress panel
 * (components/upload-tasks/UploadTasksPanel.vue). Lives in a store rather than
 * the knowledge base page so uploads keep going, and stay visible, when the
 * user navigates elsewhere in the app. Behaviour is in uploadTasksCore.ts and
 * uploadQueue.ts; this file only connects it to the API and the page.
 */
export const useUploadTasksStore = defineStore('uploadTasks', () => {
  const auth = useAuthStore()

  return createUploadTasks({
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
    emitListRefresh: (kbId, settled) => {
      window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', { detail: { kbId, settled } }))
    },
    owner: [() => auth.user?.id, () => auth.effectiveTenantId],
  })
})
