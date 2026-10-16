import { defineStore } from 'pinia'
import { computed, ref, shallowReactive, watch } from 'vue'
import type { PageContextPayload } from '@/api/chat/streame'
import {
  DEFAULT_ASSISTANT_ACCESS_LEVEL,
  capAssistantAccessLevel,
  getTenantAssistantConfig,
  isAssistantAccessLevel,
  type AssistantAccessLevel,
  type AssistantConfig,
} from '@/api/assistant'
import { useAuthStore } from './auth'

/** What the user has open, as published by the pages that know it. */
export interface AssistantPageContext {
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
}

export interface AssistantOpenRequest {
  /** Sent as the first message of a new conversation. */
  query?: string
  /** Narrows the page context for that first message only. */
  focus?: Pick<AssistantPageContext, 'wikiIssueIds'>
}

const SESSION_KEY_PREFIX = 'weknora:assistant-session'
const MODEL_KEY_PREFIX = 'weknora:assistant-model'
const ACCESS_KEY_PREFIX = 'weknora:assistant-access'
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
  return {
    agent_id: context.agentId || undefined,
    knowledge_base_id: context.knowledgeBaseId || undefined,
    knowledge_id: context.knowledgeId || undefined,
    wiki_slug: context.wikiSlug || undefined,
    wiki_issue_ids: context.wikiIssueIds?.length ? [...context.wikiIssueIds] : undefined,
  }
}

function contextKey(context: AssistantPageContext | null): string {
  if (!context) return ''
  return [context.agentId || '', context.knowledgeBaseId || '', context.knowledgeId || '', context.wikiSlug || ''].join('|')
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
    return merged.knowledgeBaseId || merged.agentId ? merged : null
  })
  const contextAttached = computed(() => !!pageContext.value && dismissedKey.value !== contextKey(pageContext.value))

  // '' leaves the choice to the run: the knowledge base's chat model, then
  // the system default.
  const modelId = ref('')

  // The conversation survives reloads within the tab and the model choice
  // persists, both scoped like the command palette's history so another user
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
  const effectiveAccessLevel = computed(() => capAssistantAccessLevel(accessLevel.value, accessCeiling.value))
  /** The workspace's name for the assistant; empty means the default. */
  const displayName = computed(() => workspaceConfig.value?.display_name?.trim() || '')

  const restore = () => {
    try {
      sessionId.value = sessionStorage.getItem(storageKey(SESSION_KEY_PREFIX)) || ''
      modelId.value = localStorage.getItem(storageKey(MODEL_KEY_PREFIX)) || ''
      const level = localStorage.getItem(storageKey(ACCESS_KEY_PREFIX))
      accessLevel.value = isAssistantAccessLevel(level) ? level : DEFAULT_ASSISTANT_ACCESS_LEVEL
    } catch {
      sessionId.value = ''
      modelId.value = ''
      accessLevel.value = DEFAULT_ASSISTANT_ACCESS_LEVEL
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

  const open = (request?: AssistantOpenRequest) => {
    if (request?.query) {
      setSession('')
      pendingRequest.value = request
    }
    visible.value = true
  }
  const close = () => {
    visible.value = false
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

  const publishPageContext = (owner: symbol, context: Partial<AssistantPageContext>) => {
    layers.set(owner, context)
  }
  const retractPageContext = (owner: symbol) => {
    layers.delete(owner)
  }
  const dismissPageContext = () => {
    dismissedKey.value = contextKey(pageContext.value)
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
    displayName,
    workspaceConfig,
    entry,
    layout,
    width,
    pendingRequest,
    pageContext,
    contextAttached,
    open,
    close,
    toggle,
    setSession,
    setModel,
    setAccessLevel,
    setWorkspaceConfig,
    loadWorkspaceConfig,
    setEntry,
    setLayout,
    setWidth,
    startNewConversation,
    takePendingRequest,
    publishPageContext,
    retractPageContext,
    dismissPageContext,
    restorePageContext,
  }
})
