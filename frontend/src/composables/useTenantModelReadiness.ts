import { computed, onMounted, ref } from 'vue'
import { useChatResourcesStore } from '@/stores/chatResources'
import { evaluateTenantModelReadiness } from '@/utils/tenantModelReadiness'

/**
 * 当前空间是否已配置好创建知识库 / 智能体所需的模型。
 *
 * 直接从 chatResources 的模型快照推导：设置页增删模型后会 `replaceModels`，
 * 这里的 computed 随之更新，不需要再监听「设置弹窗关闭」之类的间接信号去重拉。
 */
export function useTenantModelReadiness() {
  const chatResources = useChatResourcesStore()
  const loaded = ref(false)
  const loading = ref(false)

  const readiness = computed(() => evaluateTenantModelReadiness(chatResources.allModels))

  const refresh = async (force = false) => {
    loading.value = true
    try {
      await chatResources.ensureModels(force)
    } finally {
      loading.value = false
      loaded.value = true
    }
  }

  onMounted(() => {
    refresh()
  })

  const isReadyForDocumentKb = computed(() => readiness.value.isReadyForDocumentKb)

  const isReadyForAgent = computed(() => readiness.value.isReadyForAgent)

  const hasChat = computed(() => readiness.value.hasChat)

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
