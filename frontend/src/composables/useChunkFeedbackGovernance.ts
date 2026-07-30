import { computed, reactive, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import type {
  ChunkFeedbackAudit,
  ChunkFeedbackDetail,
  ChunkFeedbackListItem,
  ChunkFeedbackListParams,
} from '../api/feedback'

type GovernanceAPI = {
  list: (kbId: string, params: ChunkFeedbackListParams) => Promise<{
    data: { total: number; page: number; page_size: number; data: ChunkFeedbackListItem[] }
  }>
  detail: (kbId: string, chunkId: string) => Promise<{ data: ChunkFeedbackDetail }>
  history: (kbId: string, chunkId: string, page?: number, pageSize?: number) => Promise<{
    data: { total: number; page: number; page_size: number; data: ChunkFeedbackAudit[] }
  }>
  reset: (kbId: string, chunkId: string) => Promise<{ data: ChunkFeedbackDetail }>
}

export function useChunkFeedbackGovernance(options: {
  kbId: MaybeRefOrGetter<string>
  api?: GovernanceAPI
  autoLoad?: boolean
}) {
  const api = options.api || {
    list: async (...args) => (await import('../api/feedback')).listChunkFeedback(...args),
    detail: async (...args) => (await import('../api/feedback')).getChunkFeedbackDetail(...args),
    history: async (...args) => (await import('../api/feedback')).listChunkFeedbackHistory(...args),
    reset: async (...args) => (await import('../api/feedback')).resetChunkFeedbackGovernance(...args),
  }
  const kbId = computed(() => toValue(options.kbId).trim())
  const filters = reactive<Required<Pick<ChunkFeedbackListParams,
    'feedback_status' | 'sort_by' | 'sort_order'>> & {
      keyword: string
      optimization: 'all' | 'yes' | 'no'
    }>({
      keyword: '',
      feedback_status: 'all',
      optimization: 'all',
      sort_by: 'updated_at',
      sort_order: 'desc',
    })
  const items = ref<ChunkFeedbackListItem[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(20)
  const loading = ref(false)
  const error = ref<unknown>(null)
  const selected = ref<ChunkFeedbackDetail | null>(null)
  const history = ref<ChunkFeedbackAudit[]>([])
  const historyTotal = ref(0)
  const detailLoading = ref(false)
  const resetting = ref(false)
  let generation = 0

  const invalidate = () => {
    generation += 1
    items.value = []
    total.value = 0
    page.value = 1
    selected.value = null
    history.value = []
    historyTotal.value = 0
    error.value = null
  }

  const load = async (resetPage = false) => {
    if (!kbId.value) return false
    if (resetPage) page.value = 1
    const requestGeneration = ++generation
    const targetKB = kbId.value
    loading.value = true
    error.value = null
    try {
      const response = await api.list(targetKB, {
        page: page.value,
        page_size: pageSize.value,
        keyword: filters.keyword.trim() || undefined,
        feedback_status: filters.feedback_status,
        needs_optimization: filters.optimization === 'all'
          ? undefined
          : filters.optimization === 'yes',
        sort_by: filters.sort_by,
        sort_order: filters.sort_order,
      })
      if (requestGeneration !== generation || targetKB !== kbId.value) return true
      items.value = response.data.data || []
      total.value = response.data.total || 0
      page.value = response.data.page || page.value
      pageSize.value = response.data.page_size || pageSize.value
      return true
    } catch (cause) {
      if (requestGeneration === generation && targetKB === kbId.value) error.value = cause
      return false
    } finally {
      if (requestGeneration === generation && targetKB === kbId.value) loading.value = false
    }
  }

  const open = async (chunkId: string) => {
    if (!kbId.value || !chunkId) return false
    const requestGeneration = ++generation
    const targetKB = kbId.value
    detailLoading.value = true
    error.value = null
    try {
      const [detailResponse, historyResponse] = await Promise.all([
        api.detail(targetKB, chunkId),
        api.history(targetKB, chunkId, 1, 20),
      ])
      if (requestGeneration !== generation || targetKB !== kbId.value) return true
      selected.value = detailResponse.data
      history.value = historyResponse.data.data || []
      historyTotal.value = historyResponse.data.total || 0
      return true
    } catch (cause) {
      if (requestGeneration === generation && targetKB === kbId.value) error.value = cause
      return false
    } finally {
      if (requestGeneration === generation && targetKB === kbId.value) detailLoading.value = false
    }
  }

  const reset = async () => {
    const chunkId = selected.value?.chunk_id
    if (!kbId.value || !chunkId || resetting.value) return false
    const requestGeneration = ++generation
    const targetKB = kbId.value
    resetting.value = true
    error.value = null
    try {
      const response = await api.reset(targetKB, chunkId)
      if (requestGeneration !== generation || targetKB !== kbId.value) return true
      selected.value = response.data
      const [listResponse, historyResponse] = await Promise.all([
        api.list(targetKB, {
          page: page.value,
          page_size: pageSize.value,
          feedback_status: filters.feedback_status,
          sort_by: filters.sort_by,
          sort_order: filters.sort_order,
        }),
        api.history(targetKB, chunkId, 1, 20),
      ])
      if (requestGeneration !== generation || targetKB !== kbId.value) return true
      items.value = listResponse.data.data || []
      total.value = listResponse.data.total || 0
      history.value = historyResponse.data.data || []
      historyTotal.value = historyResponse.data.total || 0
      return true
    } catch (cause) {
      if (requestGeneration === generation && targetKB === kbId.value) error.value = cause
      return false
    } finally {
      if (requestGeneration === generation && targetKB === kbId.value) resetting.value = false
    }
  }

  watch(kbId, () => {
    invalidate()
    if (options.autoLoad !== false && kbId.value) void load(true)
  }, { immediate: true })

  return {
    filters,
    items,
    total,
    page,
    pageSize,
    loading,
    error,
    selected,
    history,
    historyTotal,
    detailLoading,
    resetting,
    load,
    open,
    reset,
    close: () => {
      selected.value = null
      history.value = []
      historyTotal.value = 0
    },
  }
}
