export const MODEL_IN_USE_ERROR_CODE = 2300

export const KNOWN_MODEL_USAGE_BINDINGS = [
  'embedding_model',
  'summary_model',
  'image_processing_model',
  'vlm_model',
  'asr_model',
  'wiki_synthesis_model',
  'chat_model',
  'rerank_model',
  'query_understand_model',
  'follow_up_model',
  'extract_model',
] as const

export type KnownModelUsageBinding = typeof KNOWN_MODEL_USAGE_BINDINGS[number]

export interface ModelUsageResource {
  id: string
  name: string
  bindings: string[]
}

export interface ModelUsageDetails {
  knowledge_bases: ModelUsageResource[]
  agents: ModelUsageResource[]
  long_term_memory: {
    bindings: string[]
  }
}

export class ModelInUseError extends Error {
  readonly details: ModelUsageDetails

  constructor(details: ModelUsageDetails) {
    super('model is in use')
    this.name = 'ModelInUseError'
    this.details = details
  }
}

type UnknownRecord = Record<string, unknown>

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseBindings(value: unknown): string[] | null {
  if (!Array.isArray(value) || value.some(binding => typeof binding !== 'string' || binding.trim() === '')) {
    return null
  }
  return [...value]
}

function parseResources(value: unknown): ModelUsageResource[] | null {
  if (!Array.isArray(value)) return null

  const resources: ModelUsageResource[] = []
  for (const item of value) {
    if (!isRecord(item) || typeof item.id !== 'string' || item.id.trim() === '' || typeof item.name !== 'string') {
      return null
    }
    const bindings = parseBindings(item.bindings)
    if (!bindings || bindings.length === 0) return null
    resources.push({ id: item.id, name: item.name, bindings })
  }
  return resources
}

export function parseModelUsageDetails(value: unknown): ModelUsageDetails | null {
  if (!isRecord(value) || !isRecord(value.long_term_memory)) return null

  const knowledgeBases = parseResources(value.knowledge_bases)
  const agents = parseResources(value.agents)
  const memoryBindings = parseBindings(value.long_term_memory.bindings)
  if (!knowledgeBases || !agents || !memoryBindings) return null

  const details = {
    knowledge_bases: knowledgeBases,
    agents,
    long_term_memory: { bindings: memoryBindings },
  }
  if (knowledgeBases.length === 0 && agents.length === 0 && memoryBindings.length === 0) {
    return null
  }
  return details
}

export function modelInUseErrorFromRequest(error: unknown): ModelInUseError | null {
  if (!isRecord(error) || !isRecord(error.error) || error.error.code !== MODEL_IN_USE_ERROR_CODE) {
    return null
  }
  const details = parseModelUsageDetails(error.error.details)
  return details ? new ModelInUseError(details) : null
}

const knownBindings = new Set<string>(KNOWN_MODEL_USAGE_BINDINGS)

export function modelUsageBindingI18nKey(binding: string): string {
  const key = knownBindings.has(binding) ? binding : 'unknown'
  return `modelSettings.usage.bindings.${key}`
}

export type ModelUsageResourceKind = 'knowledge_base' | 'agent'

export type KnowledgeBaseModelUsageSection = 'models' | 'multimodal' | 'asr'

const knowledgeBaseUsageSections: Partial<Record<KnownModelUsageBinding, KnowledgeBaseModelUsageSection>> = {
  embedding_model: 'models',
  summary_model: 'models',
  wiki_synthesis_model: 'models',
  image_processing_model: 'multimodal',
  vlm_model: 'multimodal',
  asr_model: 'asr',
}

export function modelUsageKnowledgeBaseSection(
  bindings: readonly string[],
): KnowledgeBaseModelUsageSection {
  for (const binding of bindings) {
    const section = knowledgeBaseUsageSections[binding as KnownModelUsageBinding]
    if (section) return section
  }
  return 'models'
}

export function modelUsageResourceRoute(kind: ModelUsageResourceKind, id: string) {
  if (kind === 'knowledge_base') {
    return { path: `/platform/knowledge-bases/${encodeURIComponent(id)}` }
  }
  return {
    path: '/platform/agents',
    query: { edit: id, section: 'model' },
  }
}
