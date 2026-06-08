import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { listKnowledgeBases, getKnowledgeBaseById } from '@/api/knowledge-base'
import { listAgents, type CustomAgent } from '@/api/agent'
import { listModels, type ModelConfig } from '@/api/model'
import { listWebSearchProviders, type WebSearchProviderEntity } from '@/api/web-search-provider'
import { useOrganizationStore } from '@/stores/organization'

/** 租户级资源缓存 TTL */
const CACHE_TTL_MS = 60_000

type ResourceKey = 'knowledgeBases' | 'agents' | 'models' | 'webSearchProviders'

export type ListCreatorFilter = 'all' | 'mine' | 'others'

function isKbModelReady(kb: any): boolean {
  if (!kb.summary_model_id || kb.summary_model_id === '') return false
  const strategy = kb.indexing_strategy
  const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
  if (needsEmbedding && (!kb.embedding_model_id || kb.embedding_model_id === '')) return false
  return true
}

export const useChatResourcesStore = defineStore('chatResources', () => {
  const rawKnowledgeBases = ref<any[]>([])
  const agents = ref<CustomAgent[]>([])
  const disabledOwnAgentIds = ref<string[]>([])
  const allModels = ref<ModelConfig[]>([])
  const webSearchProviders = ref<WebSearchProviderEntity[]>([])

  const loadedAt = ref<Partial<Record<ResourceKey, number>>>({})
  const inflight = new Map<ResourceKey, Promise<void>>()

  const agentKbCache = new Map<string, { at: number; data: any[] }>()
  const agentKbInflight = new Map<string, Promise<any[]>>()
  const kbDetailCache = new Map<string, { at: number; data: any }>()
  const kbDetailInflight = new Map<string, Promise<any | null>>()

  const validKnowledgeBases = computed(() => rawKnowledgeBases.value.filter(isKbModelReady))
  const chatModels = computed(() => allModels.value.filter((m) => m.type === 'KnowledgeQA'))

  function isFresh(key: ResourceKey): boolean {
    const at = loadedAt.value[key]
    return !!at && Date.now() - at < CACHE_TTL_MS
  }

  async function runOnce(key: ResourceKey, force: boolean, loader: () => Promise<void>): Promise<void> {
    if (!force && isFresh(key)) return
    const existing = inflight.get(key)
    if (existing) return existing
    const p = loader().finally(() => {
      inflight.delete(key)
    })
    inflight.set(key, p)
    return p
  }

  /**
   * 知识库列表（支持 creator 筛选）。creator=all 时写入缓存供对话页复用。
   */
  async function fetchKnowledgeBasesForList(
    params?: { creator?: ListCreatorFilter },
    force = false,
  ): Promise<any[]> {
    const creator = params?.creator ?? 'all'
    if (!force && creator === 'all' && isFresh('knowledgeBases')) {
      return rawKnowledgeBases.value
    }

    const res: any = await listKnowledgeBases(
      creator === 'all' ? undefined : { creator },
    )
    const data = res?.data && Array.isArray(res.data) ? res.data : []

    if (creator === 'all') {
      rawKnowledgeBases.value = data
      loadedAt.value.knowledgeBases = Date.now()
      const orgStore = useOrganizationStore()
      await orgStore.fetchSharedKnowledgeBases({ force })
    }

    return data
  }

  async function ensureKnowledgeBases(force = false): Promise<void> {
    await fetchKnowledgeBasesForList({ creator: 'all' }, force)
  }

  /**
   * 智能体列表（支持 creator 筛选）。creator=all 时写入缓存。
   */
  async function fetchAgentsForList(
    params?: { creator?: ListCreatorFilter },
    force = false,
  ): Promise<{ data: CustomAgent[]; disabled_own_agent_ids: string[] }> {
    const creator = params?.creator ?? 'all'
    if (!force && creator === 'all' && isFresh('agents')) {
      return { data: agents.value, disabled_own_agent_ids: disabledOwnAgentIds.value }
    }

    const orgStore = useOrganizationStore()
    const [agentsRes] = await Promise.all([
      listAgents(creator === 'all' ? undefined : { creator }),
      orgStore.fetchSharedAgents({ force }),
    ])
    const res = agentsRes as { data?: CustomAgent[]; disabled_own_agent_ids?: string[] }
    const data = res.data || []

    if (creator === 'all') {
      agents.value = data
      disabledOwnAgentIds.value = res.disabled_own_agent_ids || []
      loadedAt.value.agents = Date.now()
    }

    return { data, disabled_own_agent_ids: res.disabled_own_agent_ids || [] }
  }

  async function ensureAgents(force = false): Promise<void> {
    await fetchAgentsForList({ creator: 'all' }, force)
  }

  async function ensureModels(force = false): Promise<void> {
    return runOnce('models', force, async () => {
      const models = await listModels()
      allModels.value = Array.isArray(models) ? models : []
      loadedAt.value.models = Date.now()
    })
  }

  /** @deprecated 使用 ensureModels；保留别名供对话输入栏调用 */
  async function ensureChatModels(force = false): Promise<void> {
    return ensureModels(force)
  }

  async function ensureWebSearchProviders(force = false): Promise<void> {
    return runOnce('webSearchProviders', force, async () => {
      const response = await listWebSearchProviders()
      const providers = (response as any)?.data
      webSearchProviders.value = Array.isArray(providers) ? providers : []
      loadedAt.value.webSearchProviders = Date.now()
    })
  }

  /** 并行预取对话输入栏及列表页常用的租户级资源 */
  async function prefetchChatInput(force = false): Promise<void> {
    const orgStore = useOrganizationStore()
    await Promise.all([
      ensureKnowledgeBases(force),
      ensureAgents(force),
      ensureModels(force),
      ensureWebSearchProviders(force),
      orgStore.fetchOrganizations({ force }),
    ])
  }

  async function ensureAgentKnowledgeBases(agentId: string, force = false): Promise<any[]> {
    const cached = agentKbCache.get(agentId)
    if (!force && cached && Date.now() - cached.at < CACHE_TTL_MS) {
      return cached.data
    }
    const existing = agentKbInflight.get(agentId)
    if (existing) return existing

    const p = (async () => {
      try {
        const res: any = await listKnowledgeBases({ agent_id: agentId })
        const list = res?.data && Array.isArray(res.data) ? res.data : []
        agentKbCache.set(agentId, { at: Date.now(), data: list })
        return list
      } finally {
        agentKbInflight.delete(agentId)
      }
    })()
    agentKbInflight.set(agentId, p)
    return p
  }

  /** 单个知识库详情（侧栏 + 详情页共用，去重并发请求） */
  async function fetchKnowledgeBaseById(kbId: string, force = false): Promise<any | null> {
    if (!kbId) return null
    const cached = kbDetailCache.get(kbId)
    if (!force && cached && Date.now() - cached.at < CACHE_TTL_MS) {
      return cached.data
    }
    const existing = kbDetailInflight.get(kbId)
    if (existing) return existing

    const p = (async () => {
      try {
        const res: any = await getKnowledgeBaseById(kbId)
        const data = res?.data ?? null
        if (data) {
          kbDetailCache.set(kbId, { at: Date.now(), data })
        }
        return data
      } catch {
        return null
      } finally {
        kbDetailInflight.delete(kbId)
      }
    })()
    kbDetailInflight.set(kbId, p)
    return p
  }

  function invalidateKnowledgeBaseDetail(kbId?: string) {
    if (kbId) {
      kbDetailCache.delete(kbId)
      kbDetailInflight.delete(kbId)
    } else {
      kbDetailCache.clear()
      kbDetailInflight.clear()
    }
  }

  function invalidate(...keys: ResourceKey[]) {
    if (keys.length === 0) {
      loadedAt.value = {}
      rawKnowledgeBases.value = []
      agents.value = []
      disabledOwnAgentIds.value = []
      allModels.value = []
      webSearchProviders.value = []
      agentKbCache.clear()
      invalidateKnowledgeBaseDetail()
      return
    }
    keys.forEach((k) => {
      delete loadedAt.value[k]
    })
    if (keys.includes('knowledgeBases')) {
      agentKbCache.clear()
      invalidateKnowledgeBaseDetail()
    }
  }

  return {
    rawKnowledgeBases,
    validKnowledgeBases,
    agents,
    disabledOwnAgentIds,
    allModels,
    chatModels,
    webSearchProviders,
    isFresh,
    fetchKnowledgeBasesForList,
    fetchAgentsForList,
    ensureKnowledgeBases,
    ensureAgents,
    ensureModels,
    ensureChatModels,
    ensureWebSearchProviders,
    ensureAgentKnowledgeBases,
    prefetchChatInput,
    fetchKnowledgeBaseById,
    invalidateKnowledgeBaseDetail,
    invalidate,
  }
})
