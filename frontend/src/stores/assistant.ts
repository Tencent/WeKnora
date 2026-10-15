import { defineStore } from 'pinia'
import { computed, ref, shallowReactive, watch } from 'vue'
import type { PageContextPayload } from '@/api/chat/streame'
import { useAuthStore } from './auth'

/** What the user has open, as published by the pages that know it. */
export interface AssistantPageContext {
  knowledgeBaseId: string
  knowledgeBaseName?: string
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

export function toPageContextPayload(context: AssistantPageContext): PageContextPayload {
  return {
    knowledge_base_id: context.knowledgeBaseId,
    knowledge_id: context.knowledgeId || undefined,
    wiki_slug: context.wikiSlug || undefined,
    wiki_issue_ids: context.wikiIssueIds?.length ? [...context.wikiIssueIds] : undefined,
  }
}

function contextKey(context: AssistantPageContext | null): string {
  if (!context) return ''
  return [context.knowledgeBaseId, context.knowledgeId || '', context.wikiSlug || ''].join('|')
}

/**
 * Pinia store behind the global WeKnora assistant drawer (rendered once in
 * platform/index.vue). Pages publish what is open through
 * useAssistantPageContext; anything can open the drawer with open().
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
    return merged.knowledgeBaseId ? (merged as AssistantPageContext) : null
  })
  const contextAttached = computed(() => !!pageContext.value && dismissedKey.value !== contextKey(pageContext.value))

  // The conversation survives reloads within the tab, scoped like the
  // command palette's history so another user or workspace never inherits it.
  const storageKey = (): string => {
    const auth = useAuthStore()
    return `${SESSION_KEY_PREFIX}:${auth.user?.id || 'anon'}:${auth.effectiveTenantId ?? 'none'}`
  }
  const restoreSession = () => {
    try {
      sessionId.value = sessionStorage.getItem(storageKey()) || ''
    } catch {
      sessionId.value = ''
    }
  }
  const setSession = (id: string) => {
    sessionId.value = id
    try {
      if (id) sessionStorage.setItem(storageKey(), id)
      else sessionStorage.removeItem(storageKey())
    } catch {
      // sessionStorage can throw in private mode; the in-memory session still works.
    }
  }
  restoreSession()
  watch(() => {
    const auth = useAuthStore()
    return `${auth.user?.id}:${auth.effectiveTenantId}`
  }, restoreSession)

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
    pendingRequest,
    pageContext,
    contextAttached,
    open,
    close,
    toggle,
    setSession,
    startNewConversation,
    takePendingRequest,
    publishPageContext,
    retractPageContext,
    dismissPageContext,
    restorePageContext,
  }
})
