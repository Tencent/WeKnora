import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  getStorageEngineConfig,
  getStorageEngineStatus,
  getPromptTemplates,
  getParserEngines,
  getSystemInfo,
  type PromptTemplatesConfig,
  type StorageEngineStatusItem,
  type ParserEngineInfo,
  type SystemInfo,
} from '@/api/system'
import { listMCPServices, type MCPService } from '@/api/mcp-service'
import {
  listSkillCatalog,
  listSkills,
  type AgentScope,
  type SkillCatalogItem,
  type SkillInfo,
} from '@/api/skill'
import { getAgentTypePresets, getPlaceholders, type AgentTypePreset, type PlaceholdersResponse } from '@/api/agent'
import { getTenantRetrievalConfig } from '@/api/retrieval'
import { isStorageConfigDenied } from './storageEngineAccess'
import { createCachedResource } from './resourceCache'

export function pickUsableStorageProvider(
  candidate: string | undefined,
  engines: StorageEngineStatusItem[],
  allowedProviders: string[],
): string {
  const provider = candidate?.trim() || ''
  const isUsable = (name: string) => {
    if (!name) return false
    const status = engines.find((item) => item.name === name)
    if (status) return status.allowed !== false && status.available !== false
    if (engines.length > 0) return false
    if (allowedProviders.length > 0) return allowedProviders.includes(name)
    return false
  }

  if (isUsable(provider)) return provider
  const fallback = engines.find((item) => item.allowed !== false && item.available !== false)?.name
  if (fallback) return fallback
  return allowedProviders[0] || provider || 'local'
}

type EditorResourceKey =
  | 'storageEngine'
  | 'mcpServices'
  | 'skills'
  | 'skillCatalog'
  | 'agentTypePresets'
  | 'promptTemplates'
  | 'placeholders'
  | 'tenantRetrievalConfig'
  | 'parserEngines'
  | 'systemInfo'

/**
 * 智能体编辑器 / 知识库页面共用的配置类资源快照。
 * 与 chatResources 同一套约定：无 TTL，每次 ensure 发一次请求（并发共用），
 * 写操作后显式 `ensureX(true)` 或 `invalidate()`。
 */
export const useEditorResourcesStore = defineStore('editorResources', () => {
  const storageConfig = ref<Awaited<ReturnType<typeof getStorageEngineConfig>>['data'] | null>(null)
  const storageStatus = ref<StorageEngineStatusItem[]>([])
  const storageAllowedProviders = ref<string[]>([])
  const mcpServices = ref<MCPService[]>([])
  const mcpServicesScope = ref('')
  const skills = ref<SkillInfo[]>([])
  const skillsAvailable = ref(false)
  const skillsConfigId = ref('')
  const skillCatalog = ref<SkillCatalogItem[]>([])
  const agentTypePresets = ref<AgentTypePreset[]>([])
  const promptTemplates = ref<PromptTemplatesConfig | null>(null)
  const placeholders = ref<PlaceholdersResponse | null>(null)
  const tenantRetrievalConfig = ref<Record<string, unknown> | null>(null)
  const parserEngines = ref<ParserEngineInfo[]>([])
  const systemInfo = ref<SystemInfo | null>(null)

  const storageEngineResource = createCachedResource(
    async () => {
      const [configRes, statusRes] = await Promise.all([
        // The config endpoint is admin-only (it carries integration secrets),
        // while every creator — Contributors included — needs the status list
        // to pick a usable provider. A permission rejection on the config
        // call must therefore degrade to "no admin config", not break the
        // whole editor dependency chain (#2991).
        getStorageEngineConfig().catch((error: unknown) => {
          if (isStorageConfigDenied(error)) return null
          throw error
        }),
        getStorageEngineStatus(),
      ])
      return { configRes, statusRes }
    },
    ({ configRes, statusRes }) => {
      storageConfig.value = configRes?.data ?? null
      storageStatus.value = statusRes?.data?.engines ?? []
      storageAllowedProviders.value = statusRes?.data?.allowed_providers ?? []
    },
  )

  // A shared agent's skills and MCP services live in its owner's workspace and
  // are an entirely different set from this workspace's. The cache key has to
  // carry the agent, or switching agents would leave another workspace's
  // entries in the @ picker.
  function scopeKey(configId: string, agent?: AgentScope): string {
    if (!agent?.agentId || !agent?.sourceTenantId) return configId
    return `${configId}@${agent.sourceTenantId}:${agent.agentId}`
  }

  let mcpServicesRequestScope: { key: string; agent?: AgentScope } = { key: '' }
  const mcpServicesResource = createCachedResource(
    async () => {
      const scope = mcpServicesRequestScope
      const list = await listMCPServices(scope.agent)
      return { key: scope.key, list: Array.isArray(list) ? list : [] }
    },
    ({ key, list }) => {
      mcpServicesScope.value = key
      mcpServices.value = list
    },
  )

  let skillsRequestScope: { key: string; configId: string; agent?: AgentScope } = { key: '', configId: '' }
  const skillsResource = createCachedResource(
    async () => {
      const scope = skillsRequestScope
      // No sandbox config means no skills, for a shared agent too: its config
      // id is part of the agent config this caller already holds. The backend
      // re-reads it from the agent, so what is sent here only has to be
      // non-empty when the agent actually has one.
      if (!scope.configId) {
        return { key: scope.key, available: false, list: [] as SkillInfo[] }
      }
      try {
        const skillsRes = await listSkills(scope.configId, scope.agent)
        return {
          key: scope.key,
          available: skillsRes.skills_available !== false,
          list: skillsRes.data && skillsRes.data.length > 0 ? skillsRes.data : [],
        }
      } catch {
        return { key: scope.key, available: false, list: [] as SkillInfo[] }
      }
    },
    ({ key, available, list }) => {
      skillsConfigId.value = key
      skillsAvailable.value = available
      skills.value = list
    },
  )

  const skillCatalogResource = createCachedResource(
    async () => {
      const res = await listSkillCatalog()
      return Array.isArray(res?.data) ? res.data : []
    },
    (rows) => {
      skillCatalog.value = rows
    },
  )

  const agentTypePresetsResource = createCachedResource(
    async () => {
      const presetsRes: any = await getAgentTypePresets()
      return presetsRes?.data && Array.isArray(presetsRes.data) ? (presetsRes.data as AgentTypePreset[]) : []
    },
    (rows) => {
      agentTypePresets.value = rows
    },
  )

  const promptTemplatesResource = createCachedResource(
    async () => {
      const tmplRes = await getPromptTemplates()
      return tmplRes?.data ?? null
    },
    (config) => {
      promptTemplates.value = config
    },
  )

  const placeholdersResource = createCachedResource(
    async () => {
      const placeholdersRes = await getPlaceholders()
      return placeholdersRes?.data ?? null
    },
    (data) => {
      placeholders.value = data
    },
  )

  const tenantRetrievalConfigResource = createCachedResource(
    async () => {
      const retrievalRes: any = await getTenantRetrievalConfig()
      return (retrievalRes?.data ?? null) as Record<string, unknown> | null
    },
    (config) => {
      tenantRetrievalConfig.value = config
    },
  )

  const parserEnginesResource = createCachedResource(
    async () => {
      const resp = await getParserEngines()
      return resp?.data && Array.isArray(resp.data) ? resp.data : []
    },
    (rows) => {
      parserEngines.value = rows
    },
  )

  const systemInfoResource = createCachedResource(
    async () => {
      const response = await getSystemInfo()
      return response?.data ?? null
    },
    (info) => {
      systemInfo.value = info
    },
  )

  const resources: Record<EditorResourceKey, ReturnType<typeof createCachedResource>> = {
    storageEngine: storageEngineResource,
    mcpServices: mcpServicesResource,
    skills: skillsResource,
    skillCatalog: skillCatalogResource,
    agentTypePresets: agentTypePresetsResource,
    promptTemplates: promptTemplatesResource,
    placeholders: placeholdersResource,
    tenantRetrievalConfig: tenantRetrievalConfigResource,
    parserEngines: parserEnginesResource,
    systemInfo: systemInfoResource,
  }

  async function ensureStorageEngine(force = false): Promise<void> {
    return storageEngineResource.ensure(force)
  }

  function resolveUsableStorageProvider(candidate?: string): string {
    return pickUsableStorageProvider(
      candidate,
      storageStatus.value || [],
      storageAllowedProviders.value || [],
    )
  }

  async function ensureMcpServices(agent?: AgentScope, force = false): Promise<void> {
    const key = scopeKey('', agent)
    // 换了作用域：作废飞行中的旧请求，且必须排在它后面重新发。
    if (key !== mcpServicesRequestScope.key) {
      mcpServicesResource.invalidate()
      force = true
    }
    mcpServicesRequestScope = { key, agent }
    return mcpServicesResource.ensure(force)
  }

  async function ensureSkills(
    sandboxConfigId?: string,
    agent?: AgentScope,
    force = false,
  ): Promise<void> {
    const configId = sandboxConfigId?.trim() || ''
    const key = scopeKey(configId, agent)
    if (key !== skillsRequestScope.key) {
      skillsResource.invalidate()
      force = true
    }
    skillsRequestScope = { key, configId, agent }
    return skillsResource.ensure(force)
  }

  async function ensureSkillCatalog(force = false): Promise<void> {
    return skillCatalogResource.ensure(force)
  }

  async function ensureAgentTypePresets(force = false): Promise<void> {
    return agentTypePresetsResource.ensure(force)
  }

  async function ensurePromptTemplates(force = false): Promise<void> {
    return promptTemplatesResource.ensure(force)
  }

  async function ensurePlaceholders(force = false): Promise<void> {
    return placeholdersResource.ensure(force)
  }

  async function ensureTenantRetrievalConfig(force = false): Promise<void> {
    return tenantRetrievalConfigResource.ensure(force)
  }

  async function ensureParserEngines(force = false): Promise<void> {
    return parserEnginesResource.ensure(force)
  }

  async function ensureSystemInfo(force = false): Promise<void> {
    return systemInfoResource.ensure(force)
  }

  /** 智能体编辑器打开时预取的依赖（不含 IM channels / 单 KB shares） */
  async function prefetchAgentEditorDeps(force = false): Promise<void> {
    await Promise.all([
      // The editor only edits this workspace's agents, so its MCP services
      // are this workspace's too.
      ensureMcpServices(undefined, force),
      ensureAgentTypePresets(force),
      ensurePromptTemplates(force),
      ensureStorageEngine(force),
      ensurePlaceholders(force),
      ensureTenantRetrievalConfig(force),
    ])
  }

  function invalidate(...keys: EditorResourceKey[]) {
    if (keys.length === 0) {
      ;(Object.keys(resources) as EditorResourceKey[]).forEach((k) => resources[k].invalidate())
      storageConfig.value = null
      storageStatus.value = []
      storageAllowedProviders.value = []
      mcpServices.value = []
      mcpServicesScope.value = ''
      mcpServicesRequestScope = { key: '' }
      skills.value = []
      skillsAvailable.value = false
      skillsConfigId.value = ''
      skillsRequestScope = { key: '', configId: '' }
      skillCatalog.value = []
      agentTypePresets.value = []
      promptTemplates.value = null
      placeholders.value = null
      tenantRetrievalConfig.value = null
      parserEngines.value = []
      systemInfo.value = null
      return
    }
    keys.forEach((k) => resources[k].invalidate())
  }

  return {
    storageConfig,
    storageStatus,
    storageAllowedProviders,
    mcpServices,
    skills,
    skillsAvailable,
    skillCatalog,
    agentTypePresets,
    promptTemplates,
    placeholders,
    tenantRetrievalConfig,
    parserEngines,
    systemInfo,
    ensureStorageEngine,
    resolveUsableStorageProvider,
    ensureMcpServices,
    ensureSkills,
    ensureSkillCatalog,
    ensureAgentTypePresets,
    ensurePromptTemplates,
    ensurePlaceholders,
    ensureTenantRetrievalConfig,
    ensureParserEngines,
    ensureSystemInfo,
    prefetchAgentEditorDeps,
    invalidate,
  }
})
