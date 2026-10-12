import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { listKnowledgeBases, getKnowledgeBaseById } from '@/api/knowledge-base'
import { listAgents, type CustomAgent } from '@/api/agent'
import { listModels, type ModelConfig } from '@/api/model'
import { listWebSearchProviders, type WebSearchProviderEntity } from '@/api/web-search-provider'
import { isNamedSandboxBackend, listSandboxConfigs, type SandboxConfigRecord } from '@/api/system'
import { useOrganizationStore } from '@/stores/organization'
import { getCurrentLanguage } from '@/utils/request'
import { isLocalizedSnapshotUsable, shouldForceLocalizedRefetch } from './localizedResourceCache'
import { createCachedResource, createKeyedSnapshotCache } from './resourceCache'

type ResourceKey = 'knowledgeBases' | 'agents' | 'models' | 'webSearchProviders' | 'sandboxConfigs'

export type ListCreatorFilter = 'all' | 'mine' | 'others'

function isKbModelReady(kb: any): boolean {
  if (!kb.summary_model_id || kb.summary_model_id === '') return false
  const strategy = kb.indexing_strategy
  const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
  if (needsEmbedding && (!kb.embedding_model_id || kb.embedding_model_id === '')) return false
  return true
}

/**
 * 空间级资源（知识库 / 智能体 / 模型 / 联网搜索 / 沙箱后端）的共享快照。
 *
 * 没有 TTL：每次 `ensureX()` 都会发请求（同一时刻只有一个在飞行，挂载突发共用它），
 * 写操作之后由调用方 `ensureX(true)`、`replaceModels()` 或 `invalidate()` 显式刷新。
 * 页面直接读这里的 ref 渲染，旧快照在新数据到达前先撑着。
 */
export const useChatResourcesStore = defineStore('chatResources', () => {
  const rawKnowledgeBases = ref<any[]>([])
  const agents = ref<CustomAgent[]>([])
  const disabledOwnAgentIds = ref<string[]>([])
  const allModels = ref<ModelConfig[]>([])
  const webSearchProviders = ref<WebSearchProviderEntity[]>([])
  const sandboxConfigs = ref<SandboxConfigRecord[]>([])

  const validKnowledgeBases = computed(() => rawKnowledgeBases.value.filter(isKbModelReady))
  const chatModels = computed(() => allModels.value.filter((m) => m.type === 'KnowledgeQA'))

  const knowledgeBasesResource = createCachedResource(
    async () => {
      const res: any = await listKnowledgeBases()
      return res?.data && Array.isArray(res.data) ? (res.data as any[]) : []
    },
    (data) => {
      rawKnowledgeBases.value = data
    },
  )

  // 内置智能体名称/描述由后端按 Accept-Language 本地化返回；快照只对拉取时的
  // UI 语言有效。agentsLoadedLocale 必须是「请求发起时」的语言，不能在 await 之后再读。
  let agentsLoadedLocale = ''
  let agentsInflightLocale = ''
  const agentsResource = createCachedResource(
    async () => {
      const locale = getCurrentLanguage()
      agentsInflightLocale = locale
      const res = (await listAgents()) as { data?: CustomAgent[]; disabled_own_agent_ids?: string[] }
      return { data: res.data || [], disabled: res.disabled_own_agent_ids || [], locale }
    },
    ({ data, disabled, locale }) => {
      agents.value = data
      disabledOwnAgentIds.value = disabled
      agentsLoadedLocale = locale
    },
  )

  watch(
    () => getCurrentLanguage(),
    (locale) => {
      if (agentsLoadedLocale && agentsLoadedLocale !== locale) {
        agentsResource.invalidate()
        agentsLoadedLocale = ''
      }
    },
  )

  const modelsResource = createCachedResource(
    async () => {
      const models = await listModels()
      return Array.isArray(models) ? models : []
    },
    (models) => {
      allModels.value = models
    },
  )

  const webSearchProvidersResource = createCachedResource(
    async () => {
      const response = await listWebSearchProviders()
      const providers = (response as any)?.data
      return Array.isArray(providers) ? (providers as WebSearchProviderEntity[]) : []
    },
    (providers) => {
      webSearchProviders.value = providers
    },
  )

  // 失败只吞掉不抛：这是可选资源——拿不到就只剩「不启用沙箱」一项，
  // 智能体照样能编辑保存。调用方通常把它和一堆必需资源放在同一个
  // Promise.all 里，若在这里抛出，整个编辑器的依赖加载都会连坐
  // （技能可用性拿不到 ⇒ 技能配置分组直接消失）。
  const sandboxConfigsResource = createCachedResource(
    async () => {
      try {
        const res = await listSandboxConfigs()
        const rows = Array.isArray(res?.data) ? res.data : []
        return rows.filter((cfg) => isNamedSandboxBackend(cfg.sandbox_type))
      } catch {
        return [] as SandboxConfigRecord[]
      }
    },
    (rows) => {
      sandboxConfigs.value = rows
    },
  )

  const resources: Record<ResourceKey, ReturnType<typeof createCachedResource>> = {
    knowledgeBases: knowledgeBasesResource,
    agents: agentsResource,
    models: modelsResource,
    webSearchProviders: webSearchProvidersResource,
    sandboxConfigs: sandboxConfigsResource,
  }

  // 智能体可见知识库、单个知识库详情：拿到过就一直用，直到显式失效。
  // @ 提及列表会逐条补 count，这类按 key 的读取不能每次都打接口。
  const agentKbCache = createKeyedSnapshotCache(async (cacheKey: string) => {
    const [agentId, sourceTenantId] = cacheKey.split(':')
    const res: any = await listKnowledgeBases({
      agent_id: agentId,
      agent_source_tenant_id: sourceTenantId === 'current' ? undefined : sourceTenantId,
    })
    return res?.data && Array.isArray(res.data) ? (res.data as any[]) : []
  })
  const kbDetailCache = createKeyedSnapshotCache(async (kbId: string) => {
    try {
      const res: any = await getKnowledgeBaseById(kbId)
      return res?.data ?? null
    } catch {
      return null
    }
  })

  /** 是否已有快照（区别于「是否新鲜」：本 store 不再有新鲜度窗口）。 */
  function isLoaded(key: ResourceKey): boolean {
    if (key === 'agents') {
      return isLocalizedSnapshotUsable(agentsResource.isLoaded(), agentsLoadedLocale, getCurrentLanguage())
    }
    return resources[key].isLoaded()
  }

  /**
   * 知识库列表（支持 creator 筛选）。creator=all 走共享快照供对话页复用。
   */
  async function fetchKnowledgeBasesForList(
    params?: { creator?: ListCreatorFilter },
    force = false,
  ): Promise<any[]> {
    const creator = params?.creator ?? 'all'
    // 带 creator 过滤的列表是列表页专用、不进快照，直接透传请求。
    // 写操作后的强刷同时把全量快照刷一遍，否则对话输入栏会看到已删除的知识库。
    if (creator !== 'all') {
      if (force) void ensureKnowledgeBases(true).catch(() => {})
      const res: any = await listKnowledgeBases({ creator })
      return res?.data && Array.isArray(res.data) ? res.data : []
    }
    await ensureKnowledgeBases(force)
    return rawKnowledgeBases.value
  }

  // 共享资源是附带刷新：它的接口挂了不能连坐自己的列表，否则调用方的 catch
  // 会把已经拿到的自己的知识库 / 智能体一起清掉（手动文档编辑器、API 集成页）。
  // 共享列表失败时保留上一次快照，由页面自己决定要不要单独强刷。
  const swallow = (p: Promise<unknown>) => p.catch(() => undefined)

  async function ensureKnowledgeBases(force = false): Promise<void> {
    const orgStore = useOrganizationStore()
    await Promise.all([
      knowledgeBasesResource.ensure(force),
      swallow(orgStore.fetchSharedKnowledgeBases({ force })),
    ])
  }

  /**
   * 智能体列表（支持 creator 筛选）。creator=all 走共享快照。
   */
  async function fetchAgentsForList(
    params?: { creator?: ListCreatorFilter },
    force = false,
  ): Promise<{ data: CustomAgent[]; disabled_own_agent_ids: string[] }> {
    const creator = params?.creator ?? 'all'
    const orgStore = useOrganizationStore()

    // 带 creator 过滤的列表不进快照，但仍需刷新共享智能体（与全量路径保持一致）。
    if (creator !== 'all') {
      if (force) void ensureAgents(true).catch(() => {})
      const [agentsRes] = await Promise.all([
        listAgents({ creator }),
        orgStore.fetchSharedAgents({ force }),
      ])
      const res = agentsRes as { data?: CustomAgent[]; disabled_own_agent_ids?: string[] }
      return { data: res.data || [], disabled_own_agent_ids: res.disabled_own_agent_ids || [] }
    }

    await ensureAgents(force)
    return { data: agents.value, disabled_own_agent_ids: disabledOwnAgentIds.value }
  }

  async function ensureAgents(force = false): Promise<void> {
    const orgStore = useOrganizationStore()
    const locale = getCurrentLanguage()
    // 飞行中的请求是用另一种语言发起的：不能复用它，排在它后面再发一次。
    const mustForce =
      force ||
      shouldForceLocalizedRefetch(agentsResource.hasInFlightRequest(), agentsInflightLocale, locale)
    await Promise.all([
      agentsResource.ensure(mustForce),
      swallow(orgStore.fetchSharedAgents({ force })),
    ])
  }

  async function ensureModels(force = false): Promise<void> {
    return modelsResource.ensure(force)
  }

  /** 用刚拉到的列表覆盖快照（设置页增删改模型之后调用），并作废飞行中的旧请求。 */
  function replaceModels(models: ModelConfig[]) {
    allModels.value = Array.isArray(models) ? models : []
    modelsResource.markLoaded()
  }

  /** @deprecated 使用 ensureModels；保留别名供对话输入栏调用 */
  async function ensureChatModels(force = false): Promise<void> {
    return ensureModels(force)
  }

  async function ensureWebSearchProviders(force = false): Promise<void> {
    return webSearchProvidersResource.ensure(force)
  }

  /**
   * 沙箱后端配置，供智能体编辑器的后端选择器使用。
   * 不进 prefetchChatInput：只有编辑智能体时才需要，对话输入栏用不到。
   */
  async function ensureSandboxConfigs(force = false): Promise<void> {
    return sandboxConfigsResource.ensure(force)
  }

  /** 并行预取对话输入栏及列表页常用的空间级资源 */
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

  async function ensureAgentKnowledgeBases(agentId: string, sourceTenantId?: string, force = false): Promise<any[]> {
    return agentKbCache.ensure(`${agentId}:${sourceTenantId || 'current'}`, force)
  }

  /** 单个知识库详情（侧栏 + 详情页共用，去重并发请求） */
  async function fetchKnowledgeBaseById(kbId: string, force = false): Promise<any | null> {
    if (!kbId) return null
    return kbDetailCache.ensure(kbId, force)
  }

  function invalidateKnowledgeBaseDetail(kbId?: string) {
    kbDetailCache.invalidate(kbId)
  }

  function invalidate(...keys: ResourceKey[]) {
    if (keys.length === 0) {
      ;(Object.keys(resources) as ResourceKey[]).forEach((k) => resources[k].invalidate())
      rawKnowledgeBases.value = []
      agents.value = []
      disabledOwnAgentIds.value = []
      allModels.value = []
      webSearchProviders.value = []
      sandboxConfigs.value = []
      agentsLoadedLocale = ''
      agentsInflightLocale = ''
      agentKbCache.invalidate()
      invalidateKnowledgeBaseDetail()
      return
    }
    keys.forEach((k) => resources[k].invalidate())
    if (keys.includes('knowledgeBases')) {
      agentKbCache.invalidate()
      invalidateKnowledgeBaseDetail()
    }
    if (keys.includes('agents')) {
      agentsLoadedLocale = ''
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
    sandboxConfigs,
    isLoaded,
    fetchKnowledgeBasesForList,
    fetchAgentsForList,
    ensureKnowledgeBases,
    ensureAgents,
    ensureModels,
    replaceModels,
    ensureChatModels,
    ensureWebSearchProviders,
    ensureSandboxConfigs,
    ensureAgentKnowledgeBases,
    prefetchChatInput,
    fetchKnowledgeBaseById,
    invalidateKnowledgeBaseDetail,
    invalidate,
  }
})
