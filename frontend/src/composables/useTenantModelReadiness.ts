import { computed, onMounted, ref, watch } from 'vue'
import { useUIStore } from '@/stores/ui'
import {
  fetchTenantModelReadiness,
  type TenantModelReadiness,
} from '@/utils/tenantModelReadiness'

export function useTenantModelReadiness() {
  const uiStore = useUIStore()
  const readiness = ref<TenantModelReadiness | null>(null)
  const loaded = ref(false)
  const loading = ref(false)

  const refresh = async () => {
    loading.value = true
    try {
      readiness.value = await fetchTenantModelReadiness()
    } finally {
      loading.value = false
      loaded.value = true
    }
  }

  onMounted(() => {
    refresh()
  })

  watch(
    () => uiStore.showSettingsModal,
    (open, wasOpen) => {
      if (wasOpen && !open) {
        refresh()
      }
    },
  )

  const isReadyForDocumentKb = computed(
    () => readiness.value?.isReadyForDocumentKb ?? false,
  )

  const isReadyForAgent = computed(() => readiness.value?.isReadyForAgent ?? false)

  const hasChat = computed(() => readiness.value?.hasChat ?? false)

  return {
    readiness,
    loaded,
    loading,
    refresh,
    isReadyForDocumentKb,
    isReadyForAgent,
    hasChat,
  }
}
