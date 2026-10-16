import { defineStore } from 'pinia'
import { computed, ref, shallowReactive, shallowRef, watch } from 'vue'
import type { PageContextPayload, PageSelectionPayload } from '@/api/chat/streame'
import {
  DEFAULT_ASSISTANT_ACCESS_LEVEL,
  capAssistantAccessLevel,
  getTenantAssistantConfig,
  isAssistantAccessLevel,
  type AssistantAccessLevel,
  type AssistantConfig,
} from '@/api/assistant'
import { useAuthStore } from './auth'

/** Pages that tell the assistant where the user is. */
export type AssistantPageId =
  | 'knowledge_base'
  | 'wiki'
  | 'agent_editor'
  | 'settings_models'
  | 'mcp_services'
  | 'chat'

/** What the user has open, as published by the pages that know it. */
export interface AssistantPageContext {
  page?: AssistantPageId
  /** The part of the page in view, e.g. an editor tab ("tools"). */
  section?: string
  agentId?: string
  agentName?: string
  knowledgeBaseId?: string
  knowledgeBaseName?: string
  /** The knowledge base's chat model, the assistant's default on this page. */
  chatModelId?: string
  knowledgeId?: string
  knowledgeTitle?: string
  wikiSlug?: string
  wikiTitle?: string
  wikiIssueIds?: string[]
  modelId?: string
  modelName?: string
  mcpServiceId?: string
  mcpServiceName?: string
  /** A chat message the user asked the assistant about, with its session. */
  chatSessionId?: string
  messageId?: string
}

/** A new model's form, filled in by the assistant; never carries credentials. */
export interface AssistantModelDraft {
  type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'
  source?: 'remote' | 'local'
  provider?: string
  model_name?: string
  display_name?: string
  base_url?: string
}

/** A new remote MCP service's form, filled in by the assistant; never carries credentials. */
export interface AssistantMcpServiceDraft {
  name: string
  usage_instructions?: string
  transport_type: 'sse' | 'http-streamable'
  url: string
  auth_type?: '' | 'api_key' | 'oauth'
  api_key_header?: string
}

/** Text the user selected on the page beside the assistant. */
export interface AssistantPageSelection {
  text: string
  /** Set when the text is selected in a field the assistant can write to. */
  field?: string
  ref?: string
  startLine?: number
  endLine?: number
}

export function toPageSelectionPayload(selection: AssistantPageSelection): PageSelectionPayload {
  return {
    text: selection.text,
    field: selection.field || undefined,
    ref: selection.ref || undefined,
    start_line: selection.startLine || undefined,
    end_line: selection.endLine || undefined,
  }
}

/** A form the user asked the assistant to open, waiting for its page. */
export type AssistantPageDraft =
  | { kind: 'model'; draft: AssistantModelDraft }
  | { kind: 'mcp_service'; draft: AssistantMcpServiceDraft }

export interface AssistantOpenRequest {
  /** Sent as the first message of a new conversation. */
  query?: string
  /** Narrows the page context for that first message only. */
  focus?: Pick<AssistantPageContext, 'wikiIssueIds'>
}

const SESSION_KEY_PREFIX = 'weknora:assistant-session'
const VISIBLE_KEY_PREFIX = 'weknora:assistant-visible'
const MODEL_KEY_PREFIX = 'weknora:assistant-model'
const ACCESS_KEY_PREFIX = 'weknora:assistant-access'
const PAGE_ACCESS_KEY_PREFIX = 'weknora:assistant-page-access'
const LAYOUT_KEY = 'weknora:assistant-layout'
const WIDTH_KEY = 'weknora:assistant-width'
const ENTRY_KEY = 'weknora:assistant-entry'

/** Docked pushes the page aside; floating hovers over it. Neither blocks it. */
export type AssistantLayout = 'docked' | 'floating'

/**
 * Where the assistant is opened from besides the command palette, which
 * always offers it: the sidebar button, a floating button at the bottom
 * right, or nothing else.
 */
export type AssistantEntry = 'sidebar' | 'launcher' | 'palette'
const ASSISTANT_ENTRIES: AssistantEntry[] = ['sidebar', 'launcher', 'palette']

export const ASSISTANT_PANEL_MIN_WIDTH = 360
export const ASSISTANT_PANEL_MAX_WIDTH = 760
export const ASSISTANT_PANEL_DEFAULT_WIDTH = 440

function readLayout(): AssistantLayout {
  try {
    return localStorage.getItem(LAYOUT_KEY) === 'floating' ? 'floating' : 'docked'
  } catch {
    return 'docked'
  }
}

function readEntry(): AssistantEntry {
  try {
    const raw = localStorage.getItem(ENTRY_KEY) as AssistantEntry | null
    return raw && ASSISTANT_ENTRIES.includes(raw) ? raw : 'sidebar'
  } catch {
    return 'sidebar'
  }
}

function readWidth(): number {
  try {
    const raw = Number(localStorage.getItem(WIDTH_KEY))
    return Number.isFinite(raw) && raw > 0 ? raw : ASSISTANT_PANEL_DEFAULT_WIDTH
  } catch {
    return ASSISTANT_PANEL_DEFAULT_WIDTH
  }
}

export function toPageContextPayload(context: AssistantPageContext): PageContextPayload {
  const message = !!context.chatSessionId && !!context.messageId
  return {
    page: context.page || undefined,
    section: (context.page && context.section) || undefined,
    agent_id: context.agentId || undefined,
    knowledge_base_id: context.knowledgeBaseId || undefined,
    knowledge_id: context.knowledgeId || undefined,
    wiki_slug: context.wikiSlug || undefined,
    wiki_issue_ids: context.wikiIssueIds?.length ? [...context.wikiIssueIds] : undefined,
    model_id: context.modelId || undefined,
    mcp_service_id: context.mcpServiceId || undefined,
    session_id: message ? context.chatSessionId : undefined,
    message_id: message ? context.messageId : undefined,
  }
}

function contextKey(context: AssistantPageContext | null): string {
  if (!context) return ''
  return [
    context.page, context.agentId, context.knowledgeBaseId, context.knowledgeId, context.wikiSlug,
    context.modelId, context.mcpServiceId, context.messageId,
  ].map(part => part || '').join('|')
}

/** Whether a merged context names anything worth attaching. */
function hasPageContext(context: Partial<AssistantPageContext>): boolean {
  return !!(context.page || context.knowledgeBaseId || context.agentId || context.modelId ||
    context.mcpServiceId || (context.chatSessionId && context.messageId))
}

/**
 * Pinia store behind the global WeKnora assistant panel (rendered once in
 * platform/index.vue). Pages publish what is open through
 * useAssistantPageContext; anything can open the panel with open().
 */
export const useAssistantStore = defineStore('assistant', () => {
  const visible = ref(false)
  const sessionId = ref('')
  const pendingRequest = ref<AssistantOpenRequest | null>(null)
  // Publishers in mount order, so a nested page (Wiki) refines its parent (KB).
  const layers = shallowReactive(new Map<symbol, Partial<AssistantPageContext>>())
  const dismissedKey = ref('')

  const pageContext = computed<AssistantPageContext | null>(() => {
    const merged: Partial<AssistantPageContext> = {}
    for (const layer of layers.values()) Object.assign(merged, layer)
    return hasPageContext(merged) ? merged : null
  })
  // The page's name from the route, for pages that publish nothing; the
  // dialog or drawer open over the page; and the text selected on it.
  const pageTitle = ref('')
  const pageView = ref('')
  const pageSelection = shallowRef<AssistantPageSelection | null>(null)
  // Opening another page or dialog brings a dismissed context back: it is new.
  const attachKey = computed(() => `${contextKey(pageContext.value)}#${pageTitle.value}#${pageView.value}`)
  const contextAttached = computed(() =>
    (!!pageContext.value || !!pageTitle.value || !!pageView.value) && dismissedKey.value !== attachKey.value)

  // '' leaves the choice to the run: the knowledge base's chat model, then
  // the system default.
  const modelId = ref('')

  // The conversation and whether the panel is open survive reloads within
  // the tab and the model choice persists, all scoped like the command palette's history so another user
  // or workspace never inherits them.
  const storageKey = (prefix: string): string => {
    const auth = useAuthStore()
    return `${prefix}:${auth.user?.id || 'anon'}:${auth.effectiveTenantId ?? 'none'}`
  }
  // The user's own level; the workspace ceiling caps what a run gets.
  const accessLevel = ref<AssistantAccessLevel>(DEFAULT_ASSISTANT_ACCESS_LEVEL)
  // Workspace name and ceiling. Until it loads the ceiling is the default,
  // the same one the server applies to a workspace that never set it.
  const workspaceConfig = ref<AssistantConfig | null>(null)
  const accessCeiling = computed<AssistantAccessLevel>(() => {
    const level = workspaceConfig.value?.max_access_level
    return isAssistantAccessLevel(level) ? level : DEFAULT_ASSISTANT_ACCESS_LEVEL
  })
  // Whether the assistant may read and fill the page beside it.
  const pageAccess = ref(true)
  const effectiveAccessLevel = computed(() => capAssistantAccessLevel(accessLevel.value, accessCeiling.value))
  /** The workspace's name for the assistant; empty means the default. */
  const displayName = computed(() => workspaceConfig.value?.display_name?.trim() || '')

  const restore = () => {
    try {
      sessionId.value = sessionStorage.getItem(storageKey(SESSION_KEY_PREFIX)) || ''
      visible.value = sessionStorage.getItem(storageKey(VISIBLE_KEY_PREFIX)) === '1'
      modelId.value = localStorage.getItem(storageKey(MODEL_KEY_PREFIX)) || ''
      const level = localStorage.getItem(storageKey(ACCESS_KEY_PREFIX))
      accessLevel.value = isAssistantAccessLevel(level) ? level : DEFAULT_ASSISTANT_ACCESS_LEVEL
      pageAccess.value = localStorage.getItem(storageKey(PAGE_ACCESS_KEY_PREFIX)) !== '0'
    } catch {
      sessionId.value = ''
      visible.value = false
      modelId.value = ''
      accessLevel.value = DEFAULT_ASSISTANT_ACCESS_LEVEL
      pageAccess.value = true
    }
    void loadWorkspaceConfig()
  }
  const workspaceOwner = () => {
    const auth = useAuthStore()
    return auth.user?.id && auth.effectiveTenantId != null ? `${auth.user.id}:${auth.effectiveTenantId}` : ''
  }
  const loadWorkspaceConfig = async () => {
    workspaceConfig.value = null
    const owner = workspaceOwner()
    if (!owner) return
    try {
      const res = await getTenantAssistantConfig()
      // A workspace switch can complete while the request is in flight.
      if (workspaceOwner() === owner) workspaceConfig.value = res?.data || null
    } catch {
      // The defaults stand; the server applies the same ones.
    }
  }
  const setWorkspaceConfig = (config: AssistantConfig) => {
    workspaceConfig.value = { ...config }
  }
  const setAccessLevel = (level: AssistantAccessLevel) => {
    accessLevel.value = level
    try {
      localStorage.setItem(storageKey(ACCESS_KEY_PREFIX), level)
    } catch {
      // See setSession.
    }
  }
  const setPageAccess = (on: boolean) => {
    pageAccess.value = on
    try {
      if (on) localStorage.removeItem(storageKey(PAGE_ACCESS_KEY_PREFIX))
      else localStorage.setItem(storageKey(PAGE_ACCESS_KEY_PREFIX), '0')
    } catch {
      // See setSession.
    }
  }
  const setSession = (id: string) => {
    sessionId.value = id
    try {
      if (id) sessionStorage.setItem(storageKey(SESSION_KEY_PREFIX), id)
      else sessionStorage.removeItem(storageKey(SESSION_KEY_PREFIX))
    } catch {
      // Storage can throw in private mode; the in-memory value still works.
    }
  }
  const setModel = (id: string) => {
    modelId.value = id
    try {
      if (id) localStorage.setItem(storageKey(MODEL_KEY_PREFIX), id)
      else localStorage.removeItem(storageKey(MODEL_KEY_PREFIX))
    } catch {
      // See setSession.
    }
  }
  // Entry, layout and width are this browser's preference, shared by every
  // account.
  const entry = ref<AssistantEntry>(readEntry())
  const setEntry = (next: AssistantEntry) => {
    entry.value = next
    try {
      localStorage.setItem(ENTRY_KEY, next)
    } catch {
      // See setSession.
    }
  }
  const layout = ref<AssistantLayout>(readLayout())
  const width = ref(readWidth())
  const setLayout = (next: AssistantLayout) => {
    layout.value = next
    try {
      localStorage.setItem(LAYOUT_KEY, next)
    } catch {
      // See setSession.
    }
  }
  const setWidth = (next: number) => {
    width.value = Math.min(ASSISTANT_PANEL_MAX_WIDTH, Math.max(ASSISTANT_PANEL_MIN_WIDTH, Math.round(next)))
    try {
      localStorage.setItem(WIDTH_KEY, String(width.value))
    } catch {
      // See setSession.
    }
  }

  restore()
  watch(() => {
    const auth = useAuthStore()
    return `${auth.user?.id}:${auth.effectiveTenantId}`
  }, restore)

  const setVisible = (next: boolean) => {
    visible.value = next
    try {
      if (next) sessionStorage.setItem(storageKey(VISIBLE_KEY_PREFIX), '1')
      else sessionStorage.removeItem(storageKey(VISIBLE_KEY_PREFIX))
    } catch {
      // See setSession.
    }
  }
  const open = (request?: AssistantOpenRequest) => {
    if (request?.query) {
      setSession('')
      pendingRequest.value = request
    }
    setVisible(true)
  }
  const close = () => {
    setVisible(false)
  }
  const toggle = () => {
    if (visible.value) close()
    else open()
  }
  const startNewConversation = () => {
    pendingRequest.value = null
    setSession('')
  }
  const takePendingRequest = (): AssistantOpenRequest | null => {
    const request = pendingRequest.value
    pendingRequest.value = null
    return request
  }

  const setPageTitle = (title: string) => {
    pageTitle.value = title
  }
  const setPageView = (view: string) => {
    pageView.value = view
  }
  const setPageSelection = (selection: AssistantPageSelection | null) => {
    pageSelection.value = selection
  }

  // A message for the open conversation, sent by the conversation itself.
  const queuedMessage = shallowRef<{ id: number; query: string } | null>(null)
  let queuedSeq = 0
  /** Asks in the open conversation, or starts one when there is none. */
  const ask = (query: string) => {
    if (!sessionId.value) {
      open({ query })
      return
    }
    queuedMessage.value = { id: ++queuedSeq, query }
    setVisible(true)
  }
  const takeQueuedMessage = () => {
    const message = queuedMessage.value
    queuedMessage.value = null
    return message
  }
  // Bumped to move the cursor into the assistant's input box.
  const focusRequest = ref(0)
  const focusComposer = () => {
    setVisible(true)
    focusRequest.value++
  }

  const pageDraft = ref<AssistantPageDraft | null>(null)
  const setPageDraft = (draft: AssistantPageDraft | null) => {
    pageDraft.value = draft
  }
  /** Hands the waiting form to the page that shows it, once. */
  function takePageDraft(kind: 'model'): AssistantModelDraft | null
  function takePageDraft(kind: 'mcp_service'): AssistantMcpServiceDraft | null
  function takePageDraft(kind: AssistantPageDraft['kind']) {
    const waiting = pageDraft.value
    if (!waiting || waiting.kind !== kind) return null
    pageDraft.value = null
    return waiting.draft
  }

  const publishPageContext = (owner: symbol, context: Partial<AssistantPageContext>) => {
    layers.set(owner, context)
  }
  const retractPageContext = (owner: symbol) => {
    layers.delete(owner)
  }
  const dismissPageContext = () => {
    dismissedKey.value = attachKey.value
  }
  const restorePageContext = () => {
    dismissedKey.value = ''
  }

  return {
    visible,
    sessionId,
    modelId,
    accessLevel,
    accessCeiling,
    effectiveAccessLevel,
    pageAccess,
    displayName,
    workspaceConfig,
    entry,
    layout,
    width,
    pendingRequest,
    pageContext,
    contextAttached,
    pageTitle,
    pageView,
    pageSelection,
    setPageTitle,
    setPageView,
    setPageSelection,
    queuedMessage,
    ask,
    takeQueuedMessage,
    focusRequest,
    focusComposer,
    open,
    close,
    toggle,
    setSession,
    setModel,
    setAccessLevel,
    setPageAccess,
    setWorkspaceConfig,
    loadWorkspaceConfig,
    setEntry,
    setLayout,
    setWidth,
    startNewConversation,
    takePendingRequest,
    pageDraft,
    setPageDraft,
    takePageDraft,
    publishPageContext,
    retractPageContext,
    dismissPageContext,
    restorePageContext,
  }
})
