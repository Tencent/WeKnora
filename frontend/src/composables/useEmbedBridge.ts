import { onMounted, onUnmounted, ref, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  createEmbedSession,
  embedChatSessionStorageKey,
  exchangeEmbedSession,
  getEmbedConfig,
  getEmbedMessageList,
  getOrCreateEmbedVisitorId,
  isEmbedSessionToken,
  onEmbedHostContext,
  onEmbedHostLocale,
  onEmbedHostToken,
  parseEmbedTokenFromLocation,
  postEmbedBootstrapRequest,
  postEmbedReady,
  type EmbedChannelPublicConfig,
} from '@/api/embed'
import {
  readEmbedHistory, saveEmbedHistory, rememberEmbedSession, clearEmbedHistory,
  embedHistoryStorageKey, embedHistoryRevisionKey, readEmbedHistoryRevision, type EmbedHistoryEntry,
} from '@/utils/embedSessionHistory'
import { applyEmbedLocale, readEmbedLocaleFromUrl, syncEmbedLocaleFromUrl } from '@/i18n/embed'

// Persist the chat session id per channel so a page refresh resumes the same
// conversation (and its history) instead of silently starting a new session.
interface StoredSession {
  id: string
  sig: string
  /** Agent bound when the session was created; mismatch forces a new session. */
  agentId?: string
  visitorId?: string
}

function readStoredSession(channelId: string): StoredSession | null {
  try {
    const raw = window.localStorage.getItem(embedChatSessionStorageKey(channelId))
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed.id === 'string' && typeof parsed.sig === 'string' && parsed.id) {
      const agentId = typeof parsed.agentId === 'string' ? parsed.agentId.trim() : ''
      return {
        id: parsed.id,
        sig: parsed.sig,
        ...(agentId ? { agentId } : {}),
        ...(typeof parsed.visitorId === 'string' ? { visitorId: parsed.visitorId } : {}),
      }
    }
  } catch {
    // Malformed / legacy (plain-string) entry: ignore so a fresh signed
    // session is created below.
  }
  return null
}

function writeStoredSession(channelId: string, session: StoredSession | null) {
  try {
    if (session?.id) {
      window.localStorage.setItem(embedChatSessionStorageKey(channelId), JSON.stringify(session))
    } else {
      window.localStorage.removeItem(embedChatSessionStorageKey(channelId))
    }
  } catch {
    // localStorage may be unavailable (private mode / disabled cookies).
    // Persistence is best-effort; the session still works for this load.
  }
}

// A stored session may have been deleted/expired server-side, or its signed
// handle invalidated by a channel token rotation. Probe it cheaply (limit=1)
// with the stored signature before reusing — the backend rejects stale/foreign
// ids and bad signatures with 4xx, which surfaces here as a thrown error.
async function isStoredSessionValid(
  channelId: string,
  apiToken: string,
  session: StoredSession,
): Promise<boolean> {
  try {
    await getEmbedMessageList(channelId, apiToken, session.id, 1, undefined, session.sig)
    return true
  } catch (error) {
    if (isUnavailableSession(error)) return false
    // A temporary outage is not evidence that the signed handle is invalid.
    throw error
  }
}

function isUnavailableSession(error: unknown): boolean {
  const failure = error as { $httpStatus?: number; status?: number; response?: { status?: number } } | null
  const status = failure?.$httpStatus ?? failure?.response?.status ?? failure?.status
  return status === 403 || status === 404 || status === 410
}

export function useEmbedBridge(channelId: Ref<string>) {
  const { locale: activeLocale, t } = useI18n()
  const route = useRoute()

  const token = ref('')
  const config = ref<EmbedChannelPublicConfig | null>(null)
  const sessionId = ref('')
  const sessionSig = ref('')
  const visitorId = ref('')
  const loadError = ref('')
  const awaitingToken = ref(false)
  const bootstrapping = ref(false)
  const hostContext = ref<Record<string, unknown>>({})
  const sessionHistory = ref<EmbedHistoryEntry[]>([])
  const sessionBusy = ref(false)
  const historyError = ref('')
  const sessionHasMessages = ref(false)
  let operation = 0
  const historyScope = () => ({
    channelId: channelId.value, agentId: config.value?.agent_id || '', visitorId: visitorId.value,
  })
  let historyRevision = ''
  let suppressedSessionId = ''
  const syncHistoryClear = () => {
    const revision = readEmbedHistoryRevision(historyScope())
    if (revision !== historyRevision) {
      historyRevision = revision
      operation++
      sessionBusy.value = false
      sessionHistory.value = readEmbedHistory(historyScope())
      // Keep the open chat usable, but do not resurrect its cleared handle.
      suppressedSessionId = sessionId.value
    }
  }
  const onHistoryStorage = (event: StorageEvent) => {
    try {
      if (event.storageArea && event.storageArea !== window.localStorage) return
    } catch { return }
    if (event.key === null) {
      operation++
      sessionBusy.value = false
      suppressedSessionId = sessionId.value
    }
    if (event.key === null || event.key === embedHistoryRevisionKey(historyScope())
      || event.key === embedHistoryStorageKey(historyScope())) {
      syncHistoryClear()
      sessionHistory.value = readEmbedHistory(historyScope())
    }
  }
  const rememberCurrentSession = (title?: string) => {
    syncHistoryClear()
    if (!sessionHasMessages.value || !sessionId.value || suppressedSessionId === sessionId.value) return
    const previous = sessionHistory.value.find(item => item.id === sessionId.value)
    sessionHistory.value = rememberEmbedSession(historyScope(), {
      id: sessionId.value, sig: sessionSig.value, title: title?.trim() || previous?.title || '', updatedAt: Date.now(),
    }, sessionHistory.value)
    writeStoredSession(channelId.value, {
      id: sessionId.value, sig: sessionSig.value, agentId: config.value?.agent_id || '', visitorId: visitorId.value,
    })
  }
  const setSessionHasMessages = (hasMessages: boolean) => {
    sessionHasMessages.value = hasMessages
    if (hasMessages) rememberCurrentSession()
  }

  let removeHostListener: (() => void) | null = null
  let removeLocaleListener: (() => void) | null = null
  let removeTokenListener: (() => void) | null = null
  let bootstrapped = false
  let hostLocalePinned = false
  if (typeof window !== 'undefined') {
    hostLocalePinned = Boolean(readEmbedLocaleFromUrl())
    if (hostLocalePinned) {
      syncEmbedLocaleFromUrl(activeLocale)
    }
  }

  const bootstrap = async (embedToken: string) => {
    const id = channelId.value
    if (!id || !embedToken || bootstrapped) return
    bootstrapped = true
    loadError.value = ''
    awaitingToken.value = false
    bootstrapping.value = true
    token.value = embedToken
    visitorId.value = getOrCreateEmbedVisitorId(id)

    try {
      let apiToken = embedToken
      // Secure mode: the host already handed us a short-lived session token
      // (minted server-side from the publish token). Use it directly — the
      // exchange endpoint only accepts publish tokens and would reject this.
      if (!isEmbedSessionToken(embedToken)) {
        try {
          const exchangeRes = await exchangeEmbedSession(id, embedToken)
          if (exchangeRes?.data?.session_token) {
            apiToken = exchangeRes.data.session_token
          } else if (!import.meta.env.DEV) {
            // Fail closed in production: a missing session token must not silently
            // fall back to the long-lived publish token.
            throw new Error('embed session exchange returned no token')
          }
        } catch (exchangeErr) {
          // In production we refuse to downgrade to the publish token; only the
          // dev build keeps the convenience fallback for local testing.
          if (!import.meta.env.DEV) {
            throw exchangeErr
          }
        }
      }

      const res = await getEmbedConfig(id, apiToken)
      if (!res?.success || !res.data) {
        loadError.value = t('embedPublish.invalidChannel')
        bootstrapped = false
        return
      }
      config.value = res.data

      if (res.data.default_locale && !hostLocalePinned) {
        applyEmbedLocale(res.data.default_locale, activeLocale)
      }

      // Resume a persisted session when still valid and still bound to the same
      // agent; otherwise create a fresh one (e.g. after rebinding the channel).
      const configAgentId = String(res.data.agent_id || '').trim()
      let resolved: StoredSession | null = null
      const stored = readStoredSession(id)
      const revisionBeforeValidation = readEmbedHistoryRevision(historyScope())
      const agentMatches = !stored?.agentId || !configAgentId || stored.agentId === configAgentId
      if (stored && agentMatches && (!stored.visitorId || stored.visitorId === visitorId.value)
        && (await isStoredSessionValid(id, apiToken, stored))
        && revisionBeforeValidation === readEmbedHistoryRevision(historyScope())) {
        resolved = { ...stored, agentId: configAgentId || stored.agentId }
      } else {
        const sessionRes = await createEmbedSession(id, apiToken)
        const newId = sessionRes?.data?.id || ''
        if (newId) {
          resolved = { id: newId, sig: sessionRes?.data?.sig || '', agentId: configAgentId }
        }
      }
      if (!resolved) {
        loadError.value = t('embedPublish.sessionFailed')
        bootstrapped = false
        return
      }
      resolved.visitorId = visitorId.value
      historyRevision = readEmbedHistoryRevision(historyScope())
      sessionHistory.value = readEmbedHistory(historyScope())
      sessionId.value = resolved.id
      sessionSig.value = resolved.sig
      writeStoredSession(id, resolved)
      token.value = apiToken
      postEmbedReady(id)
    } catch (e: unknown) {
      bootstrapped = false
      const msg = String((e as { message?: string })?.message || '')
      if (msg.includes('disabled')) {
        loadError.value = t('embedPublish.channelDisabled')
      } else if (msg.includes('failed to create session')) {
        loadError.value = t('embedPublish.sessionFailed')
      } else {
        loadError.value = msg || t('embedPublish.loadError')
      }
    } finally {
      bootstrapping.value = false
    }
  }

  // Serialize navigation, and only replace the active signed handle after a
  // successful response. Failed or stale requests must leave the current chat intact.
  const startNewSession = async (): Promise<boolean> => {
    const id = channelId.value
    const apiToken = token.value
    if (!id || !apiToken || sessionBusy.value) return false
    syncHistoryClear()
    const currentOperation = ++operation
    sessionBusy.value = true
    historyError.value = ''
    try {
      const sessionRes = await createEmbedSession(id, apiToken)
      const newId = sessionRes?.data?.id || ''
      if (!newId) throw new Error('missing session')
      syncHistoryClear()
      if (currentOperation !== operation || id !== channelId.value || apiToken !== token.value) return false
      const next: StoredSession = {
        id: newId, sig: sessionRes?.data?.sig || '',
        agentId: config.value?.agent_id || '', visitorId: visitorId.value,
      }
      sessionHasMessages.value = false
      sessionSig.value = next.sig
      sessionId.value = next.id
      writeStoredSession(id, next)
      return true
    } catch {
      if (currentOperation === operation) historyError.value = t('embedPublish.historyLoadFailed')
      return false
    } finally {
      if (currentOperation === operation) sessionBusy.value = false
    }
  }

  const switchHistorySession = async (targetId: string): Promise<boolean> => {
    if (sessionBusy.value) return false
    syncHistoryClear()
    const entry = sessionHistory.value.find(item => item.id === targetId)
    if (!entry) return false
    if (targetId === sessionId.value) return true
    const id = channelId.value
    const apiToken = token.value
    const currentOperation = ++operation
    sessionBusy.value = true
    historyError.value = ''
    try {
      await getEmbedMessageList(id, apiToken, entry.id, 1, undefined, entry.sig)
      syncHistoryClear()
      if (currentOperation !== operation || id !== channelId.value || apiToken !== token.value) return false
      sessionHasMessages.value = false
      sessionSig.value = entry.sig
      sessionId.value = entry.id
      writeStoredSession(id, { ...entry, agentId: config.value?.agent_id || '', visitorId: visitorId.value })
      return true
    } catch (error: unknown) {
      syncHistoryClear()
      if (currentOperation !== operation || id !== channelId.value) return false
      // A timeout or server error proves nothing about the saved session.
      if (isUnavailableSession(error)) {
        sessionHistory.value = saveEmbedHistory(historyScope(), sessionHistory.value.filter(item => item.id !== entry.id))
        historyError.value = t('embedPublish.historyUnavailable')
      } else {
        historyError.value = t('embedPublish.historyLoadFailed')
      }
      return false
    } finally {
      if (currentOperation === operation) sessionBusy.value = false
    }
  }

  const clearSessionHistory = async (): Promise<boolean> => {
    // Preserve handles if creating the fresh replacement fails.
    if (!await startNewSession()) return false
    sessionHistory.value = clearEmbedHistory(historyScope())
    historyRevision = readEmbedHistoryRevision(historyScope())
    writeStoredSession(channelId.value, null)
    return true
  }

  const start = async () => {
    removeHostListener = onEmbedHostContext((payload) => {
      hostContext.value = { ...hostContext.value, ...payload }
    })

    removeLocaleListener = onEmbedHostLocale((locale) => {
      hostLocalePinned = true
      applyEmbedLocale(locale, activeLocale)
    })

    removeTokenListener = onEmbedHostToken((providedToken, providedChannelId) => {
      if (providedChannelId && providedChannelId !== channelId.value) return
      bootstrap(providedToken)
    })

    if (!channelId.value) {
      loadError.value = t('embedPublish.missingChannel')
      return
    }

    const initialToken = String(route.query.token || '') || parseEmbedTokenFromLocation()
    if (initialToken) {
      await bootstrap(initialToken)
      return
    }

    if (window.parent !== window) {
      awaitingToken.value = true
      postEmbedBootstrapRequest(channelId.value)
      return
    }

    loadError.value = t('embedPublish.missingChannel')
  }

  onMounted(() => {
    window.addEventListener('storage', onHistoryStorage)
    start()
  })

  onUnmounted(() => {
    operation++
    window.removeEventListener('storage', onHistoryStorage)
    removeHostListener?.()
    removeLocaleListener?.()
    removeTokenListener?.()
  })

  return {
    token,
    config,
    sessionId,
    sessionSig,
    visitorId,
    loadError,
    awaitingToken,
    bootstrapping,
    hostContext,
    startNewSession,
    retryBootstrap: () => bootstrap(token.value),
    sessionHistory,
    sessionBusy,
    historyError,
    switchHistorySession,
    clearSessionHistory,
    rememberCurrentSession,
    setSessionHasMessages,
  }
}
