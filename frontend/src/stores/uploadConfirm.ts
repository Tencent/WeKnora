import { defineStore } from 'pinia'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

export type UploadConfirmMode = 'file' | 'url' | 'manual' | 'reparse'

export interface UploadConfirmManualSource {
  kbId: string
  knowledgeId?: string
  title: string
  content: string
  tagIds?: string[]
}

export interface UploadConfirmReparseSource {
  knowledgeId: string
  fileName?: string
  fileType?: string
  processOverrides?: KnowledgeProcessOverrides | null
}

export interface UploadConfirmResult {
  processConfig: KnowledgeProcessOverrides
  mode: UploadConfirmMode
  files?: File[]
  urls?: string[]
  manual?: UploadConfirmManualSource
  reparse?: UploadConfirmReparseSource
}

export interface OpenUploadConfirmOptions {
  mode: UploadConfirmMode
  kbInfo: any
  files?: File[]
  urls?: string[]
  manual?: UploadConfirmManualSource
  reparse?: UploadConfirmReparseSource
  acceptFileTypes?: string
  supportedFileTypes?: string[]
}

export const useUploadConfirmStore = defineStore('uploadConfirm', {
  state: () => ({
    visible: false,
    mode: 'file' as UploadConfirmMode,
    kbInfo: null as any,
    files: [] as File[],
    urls: [] as string[],
    manual: null as UploadConfirmManualSource | null,
    reparse: null as UploadConfirmReparseSource | null,
    acceptFileTypes: '',
    supportedFileTypes: [] as string[],
    pendingResolve: null as ((value: UploadConfirmResult) => void) | null,
    pendingReject: null as (() => void) | null,
  }),

  actions: {
    open(options: OpenUploadConfirmOptions): Promise<UploadConfirmResult> {
      return new Promise((resolve, reject) => {
        this.visible = true
        this.mode = options.mode
        this.kbInfo = options.kbInfo
        this.files = options.files ? [...options.files] : []
        this.urls = options.urls ? [...options.urls] : []
        this.manual = options.manual || null
        this.reparse = options.reparse || null
        this.acceptFileTypes = options.acceptFileTypes || ''
        this.supportedFileTypes = options.supportedFileTypes ? [...options.supportedFileTypes] : []
        this.pendingResolve = resolve
        this.pendingReject = reject
      })
    },

    resolveConfirm(payload: UploadConfirmResult) {
      this.pendingResolve?.(payload)
      this.reset()
    },

    rejectConfirm() {
      this.pendingReject?.()
      this.reset()
    },

    reset() {
      this.visible = false
      this.mode = 'file'
      this.kbInfo = null
      this.files = []
      this.urls = []
      this.manual = null
      this.reparse = null
      this.acceptFileTypes = ''
      this.supportedFileTypes = []
      this.pendingResolve = null
      this.pendingReject = null
    },
  },
})
