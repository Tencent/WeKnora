/** Plugin configuration: shape, defaults, and load-time validation.
 *
 * Mirrors the configuration layer of `dsh-weknora` (WeKnora's DeepSeek Harness
 * plugin): the same fields, defaults and validation, so deployments can run
 * both agents against one set of environment variables.
 */

/** Which tools the plugin registers. Omitted entries keep their default. */
export interface ToolToggles {
  listKnowledgeBases?: boolean
  search?: boolean
  readDocument?: boolean
  ask?: boolean
}

/** User-supplied configuration, as written in the opencode plugin options. */
export interface Config {
  /** WeKnora API root, with or without the `/api/v1` suffix. */
  baseUrl?: string
  /** Tenant or platform API key, sent as `X-API-Key`. */
  apiKey?: string
  /** Workspace id, required only for platform-scoped API keys (`X-Tenant-ID`). */
  tenantId?: string
  /** Default knowledge-base scope when a call names none. */
  knowledgeBaseIds?: string[]
  /** Default custom agent for `weknora_ask`; omitted uses the RAG pipeline. */
  agentId?: string
  /** Default and maximum number of chunks a search returns. */
  maxResults?: number
  /** Per-chunk character budget before the plugin truncates content. */
  maxChunkChars?: number
  /** Timeout for non-streaming requests. */
  requestTimeoutMs?: number
  /** Timeout for a streamed answer, which includes model and tool time. */
  chatTimeoutMs?: number
  /**
   * `public` asks WeKnora for directly loadable file URLs in answers and
   * citations. `handle` keeps the internal `resource://` references.
   */
  resourceUrls?: 'handle' | 'public'
  /** Tool-name prefix, so two instances can serve two deployments. */
  toolPrefix?: string
  /** Per-tool registration switches. */
  tools?: ToolToggles
}

/** Configuration after defaults and validation. */
export interface ResolvedConfig {
  baseUrl: string
  apiKey: string | undefined
  tenantId: string | undefined
  knowledgeBaseIds: string[]
  agentId: string | undefined
  maxResults: number
  maxChunkChars: number
  requestTimeoutMs: number
  chatTimeoutMs: number
  resourceUrls: 'handle' | 'public'
  toolPrefix: string
  tools: Required<ToolToggles>
}

const DEFAULTS = {
  baseUrl: 'http://localhost:8080/api/v1',
  maxResults: 8,
  maxChunkChars: 1200,
  requestTimeoutMs: 30_000,
  chatTimeoutMs: 300_000,
  resourceUrls: 'public',
  toolPrefix: 'weknora',
} as const

/** Thrown when a plugin row configures this plugin in a way it cannot serve. */
export class ConfigError extends Error {
  constructor(violations: string[]) {
    super(`opencode-weknora configuration is invalid:\n  - ${violations.join('\n  - ')}`)
    this.name = 'ConfigError'
  }
}

const API_ROOT = /\/api\/v\d+$/
const TOOL_PREFIX = /^[a-z][a-z0-9_]*$/

/** Append the API root when a bare deployment URL was given. */
export function normalizeBaseUrl(raw: string): string {
  const trimmed = raw.trim().replace(/\/+$/, '')
  if (trimmed === '') return DEFAULTS.baseUrl
  return API_ROOT.test(trimmed) ? trimmed : `${trimmed}/api/v1`
}

function positiveInt(value: unknown, field: string, violations: string[]): number | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0 || Math.floor(value) !== value) {
    violations.push(`${field} must be a positive integer`)
    return undefined
  }
  return value
}

function stringList(value: unknown, field: string, violations: string[]): string[] | undefined {
  if (value === undefined) return undefined
  if (!Array.isArray(value) || !value.every(entry => typeof entry === 'string' && entry.trim() !== '')) {
    violations.push(`${field} must be an array of non-empty strings`)
    return undefined
  }
  return value.map(entry => entry.trim())
}

function optionalString(value: unknown, field: string, violations: string[]): string | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'string' || value.trim() === '') {
    violations.push(`${field} must be a non-empty string`)
    return undefined
  }
  return value.trim()
}

function toggle(value: unknown, field: string, fallback: boolean, violations: string[]): boolean {
  if (value === undefined) return fallback
  if (typeof value !== 'boolean') {
    violations.push(`tools.${field} must be a boolean`)
    return fallback
  }
  return value
}

/** Validate a user-supplied config and fill in the defaults. */
export function resolveConfig(raw: Config | undefined): ResolvedConfig {
  const violations: string[] = []
  const source = raw ?? {}
  const baseUrlRaw = optionalString(source.baseUrl, 'baseUrl', violations)
  const apiKey = optionalString(source.apiKey, 'apiKey', violations)
  const tenantId = optionalString(source.tenantId, 'tenantId', violations)
  const knowledgeBaseIds = stringList(source.knowledgeBaseIds, 'knowledgeBaseIds', violations)
  const agentId = optionalString(source.agentId, 'agentId', violations)
  const maxResults = positiveInt(source.maxResults, 'maxResults', violations)
  const maxChunkChars = positiveInt(source.maxChunkChars, 'maxChunkChars', violations)
  const requestTimeoutMs = positiveInt(source.requestTimeoutMs, 'requestTimeoutMs', violations)
  const chatTimeoutMs = positiveInt(source.chatTimeoutMs, 'chatTimeoutMs', violations)
  const toolPrefix = optionalString(source.toolPrefix, 'toolPrefix', violations)
  if (toolPrefix !== undefined && !TOOL_PREFIX.test(toolPrefix)) {
    violations.push('toolPrefix must match ^[a-z][a-z0-9_]*$')
  }
  let resourceUrls: 'handle' | 'public' | undefined
  if (source.resourceUrls !== undefined) {
    if (source.resourceUrls === 'handle' || source.resourceUrls === 'public') {
      resourceUrls = source.resourceUrls
    } else {
      violations.push("resourceUrls must be 'public' or 'handle'")
    }
  }
  const tools = source.tools ?? {}
  const toolToggles = {
    listKnowledgeBases: toggle(tools.listKnowledgeBases, 'listKnowledgeBases', true, violations),
    search: toggle(tools.search, 'search', true, violations),
    readDocument: toggle(tools.readDocument, 'readDocument', true, violations),
    ask: toggle(tools.ask, 'ask', true, violations),
  }
  if (violations.length > 0) throw new ConfigError(violations)
  return {
    baseUrl: normalizeBaseUrl(baseUrlRaw ?? DEFAULTS.baseUrl),
    apiKey,
    tenantId,
    knowledgeBaseIds: knowledgeBaseIds ?? [],
    agentId,
    maxResults: maxResults ?? DEFAULTS.maxResults,
    maxChunkChars: maxChunkChars ?? DEFAULTS.maxChunkChars,
    requestTimeoutMs: requestTimeoutMs ?? DEFAULTS.requestTimeoutMs,
    chatTimeoutMs: chatTimeoutMs ?? DEFAULTS.chatTimeoutMs,
    resourceUrls: resourceUrls ?? DEFAULTS.resourceUrls,
    toolPrefix: toolPrefix ?? DEFAULTS.toolPrefix,
    tools: toolToggles,
  }
}

/**
 * Read the `WEKNORA_*` environment variables. opencode plugin options take
 * precedence (they are merged over this), so one environment can serve many
 * agents while a single project overrides just what differs.
 */
export function configFromEnv(env: Record<string, string | undefined> = process.env): Config {
  const config: Config = {}
  if (env.WEKNORA_BASE_URL !== undefined) config.baseUrl = env.WEKNORA_BASE_URL
  if (env.WEKNORA_API_KEY !== undefined) config.apiKey = env.WEKNORA_API_KEY
  if (env.WEKNORA_TENANT_ID !== undefined) config.tenantId = env.WEKNORA_TENANT_ID
  if (env.WEKNORA_AGENT_ID !== undefined) config.agentId = env.WEKNORA_AGENT_ID
  if (env.WEKNORA_TOOL_PREFIX !== undefined) config.toolPrefix = env.WEKNORA_TOOL_PREFIX
  if (env.WEKNORA_KNOWLEDGE_BASE_IDS !== undefined) {
    config.knowledgeBaseIds = env.WEKNORA_KNOWLEDGE_BASE_IDS.split(',').map(entry => entry.trim()).filter(entry => entry !== '')
  }
  if (env.WEKNORA_RESOURCE_URLS === 'handle' || env.WEKNORA_RESOURCE_URLS === 'public') {
    config.resourceUrls = env.WEKNORA_RESOURCE_URLS
  }
  return config
}
