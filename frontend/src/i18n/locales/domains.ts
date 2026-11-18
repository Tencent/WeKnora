export const LOCALE_DOMAINS = [
  'common',
  'auth',
  'chat',
  'agent',
  'knowledge',
  'datasource',
  'model',
  'settings',
  'system',
] as const

export type LocaleDomain = (typeof LOCALE_DOMAINS)[number]

/**
 * Maps each top-level i18n namespace to a cohesive business domain module under
 * `src/i18n/locales/<locale>/<domain>.ts`. Any newly added namespace not yet
 * listed here deterministically falls back to `common` so pruning/regeneration
 * scripts never lose keys.
 */
export const DOMAIN_BY_NAMESPACE: Readonly<Record<string, LocaleDomain>> = {
  // common & cross-cutting shell / onboarding / navigation
  common: 'common',
  error: 'common',
  general: 'common',
  theme: 'common',
  font: 'common',
  platform: 'common',
  time: 'common',
  preview: 'common',
  promptTemplate: 'common',
  language: 'common',
  menu: 'common',
  notifications: 'common',
  toolbox: 'common',
  commandPalette: 'common',
  assistantEntryGuide: 'common',
  newUserGuide: 'common',
  contextualGuide: 'common',
  resourceSort: 'common',
  resourceOrigin: 'common',
  listSpaceSidebar: 'common',
  batchManage: 'common',
  file: 'common',
  mermaid: 'common',
  tools: 'common',

  // authentication, workspace membership & user identity
  auth: 'auth',
  authStore: 'auth',
  inviteRegister: 'auth',
  initialization: 'auth',
  userProfile: 'auth',
  tenant: 'auth',
  tenantMember: 'auth',
  tenantInvitation: 'auth',
  platformApiKeys: 'auth',
  organization: 'auth',

  // chat, mentions, artifacts & local browser preview
  chat: 'chat',
  chatHeader: 'chat',
  createChat: 'chat',
  input: 'chat',
  mentionBrowser: 'chat',
  mentionDetail: 'chat',
  artifactLibrary: 'chat',
  localBrowser: 'chat',

  // agents, agent editor, streaming timeline & automations
  agent: 'agent',
  agentEditor: 'agent',
  agentStream: 'agent',
  agentSettings: 'agent',
  automation: 'agent',
  assistant: 'agent',
  assistantSettings: 'agent',

  // knowledge bases, wiki, FAQ, chunking & multimodal pipelines
  knowledgeBase: 'knowledge',
  knowledgeList: 'knowledge',
  knowledgeEditor: 'knowledge',
  knowledgeStages: 'knowledge',
  kbSettings: 'knowledge',
  upload: 'knowledge',
  uploadTasks: 'knowledge',
  uploadConfirm: 'knowledge',
  manualEditor: 'knowledge',
  faqManager: 'knowledge',
  imageAttr: 'knowledge',
  imagePipeline: 'knowledge',

  // external connectors, integrations, embed channels & MCP
  datasource: 'datasource',
  integrations: 'datasource',
  embedPublish: 'datasource',
  mcp: 'datasource',
  mcpSettings: 'datasource',
  mcpMetadata: 'datasource',
  mcpServiceDialog: 'datasource',

  // models, vendor catalog & vector store badges
  model: 'model',
  modelCatalog: 'model',
  modelSettings: 'model',
  ollama: 'model',
  ollamaSettings: 'model',
  vectorStoreBadge: 'model',

  // workspace & engine settings panels
  settings: 'settings',
  conversationSettings: 'settings',
  webSearchSettings: 'settings',
  vectorStoreSettings: 'settings',
  memorySettings: 'settings',
  credentialVault: 'settings',
  credential: 'settings',
  envVarSettings: 'settings',
  defaultSettings: 'settings',
  memoryWorkspaceSettings: 'settings',
  chatHistorySettings: 'settings',
  retrievalSettings: 'settings',
  graphSettings: 'settings',

  // system administration, queues & document progress
  system: 'system',
  documentProgress: 'system',
}

export function resolveLocaleDomain(namespace: string): LocaleDomain {
  return DOMAIN_BY_NAMESPACE[namespace] ?? 'common'
}

export function splitLocaleBundleByDomain(
  bundle: Record<string, unknown>,
): Record<LocaleDomain, Record<string, unknown>> {
  const buckets = Object.fromEntries(
    LOCALE_DOMAINS.map((domain) => [domain, {} as Record<string, unknown>]),
  ) as Record<LocaleDomain, Record<string, unknown>>

  for (const [namespace, value] of Object.entries(bundle)) {
    const domain = resolveLocaleDomain(namespace)
    buckets[domain][namespace] = value
  }

  return buckets
}
