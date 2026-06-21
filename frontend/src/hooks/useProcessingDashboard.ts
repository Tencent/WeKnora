import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { getProcessingDashboard } from '@/api/processing-dashboard'
import type { ProcessingDashboardResponse } from '@/types/processingDashboard'
import { autoRefreshDelay } from '@/views/processingDashboard/format'

export function useProcessingDashboard() {
  const data = ref<ProcessingDashboardResponse | null>(null)
  const loading = ref(false)
  const error = ref('')
  const kbId = ref('')
  const keyword = ref('')
  const refreshSeconds = ref(10)

  let timer: number | undefined
  let latestRequestId = 0
  let keywordTimer: number | undefined

  const queueUnavailable = computed(() => {
    const state = data.value?.source?.queue_snapshot
    return state === 'degraded' || state === 'not_applicable' || state === 'partial'
  })

  const stopTimer = () => {
    if (timer) window.clearTimeout(timer)
    timer = undefined
  }

  const schedule = () => {
    stopTimer()
    const delay = autoRefreshDelay(refreshSeconds.value)
    if (!delay || document.visibilityState !== 'visible') return
    timer = window.setTimeout(() => {
      void refresh()
    }, delay)
  }

  const refresh = async () => {
    const requestId = ++latestRequestId
    const controller = new AbortController()
    loading.value = true
    error.value = ''
    try {
      const res = await getProcessingDashboard({
        kb_id: kbId.value || undefined,
        keyword: keyword.value || undefined,
        active_limit: 5,
      }, controller.signal)
      if (requestId !== latestRequestId) return
      data.value = res.data
    } catch (e: any) {
      if (requestId !== latestRequestId) return
      if (e?.name !== 'CanceledError' && e?.name !== 'AbortError') {
        error.value = e?.message || 'Failed to load'
      }
    } finally {
      if (requestId === latestRequestId) {
        loading.value = false
        schedule()
      }
    }
  }

  const setKeywordDebounced = (value: string) => {
    if (keywordTimer) window.clearTimeout(keywordTimer)
    keywordTimer = window.setTimeout(() => {
      keyword.value = value
    }, 300)
  }

  const handleVisibility = () => {
    if (document.visibilityState === 'visible') {
      void refresh()
    } else {
      stopTimer()
      latestRequestId++
    }
  }

  document.addEventListener('visibilitychange', handleVisibility)

  watch([kbId, keyword], () => {
    void refresh()
  })

  watch(refreshSeconds, schedule)

  onBeforeUnmount(() => {
    stopTimer()
    if (keywordTimer) window.clearTimeout(keywordTimer)
    latestRequestId++
    document.removeEventListener('visibilitychange', handleVisibility)
  })

  return {
    data,
    loading,
    error,
    kbId,
    keyword,
    refreshSeconds,
    queueUnavailable,
    refresh,
    setKeywordDebounced,
  }
}
