import { onMounted, onUnmounted, ref, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  createEmbedSession,
  embedChatSessionStorageKey,
  exchangeEmbedSession,
  getEmbedConfig,
  getEmbedMessageList,
  isEmbedSessionToken,
  onEmbedHostContext,
  onEmbedHostLocale,
  onEmbedHostToken,
  parseEmbedTokenFromLocation,
  postEmbedBootstrapRequest,
  postEmbedReady,
  type EmbedChannelPublicConfig,
} from '@/api/embed'
import { applyEmbedLocale, readEmbedLocaleFromUrl, syncEmbedLocaleFromUrl } from '@/i18n/embed'

// Persist the chat session id per channel so a page refresh resumes the same
// conversation (and its history) instead of silently starting a new session.
interface StoredSession {
  id: string
  sig: string
  /** Agent bound when the session was created; mismatch forces a new session. */
  agentId?: string
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
  } catch {
    return false
  }
}

export function useEmbedBridge(channelId: Ref<string>) {
  const { locale: activeLocale, t } = useI18n()
  const route = useRoute()

  const token = ref('')
  const config = ref<EmbedChannelPublicConfig | null>(null)
  const sessionId = ref('')
  const sessionSig = ref('')
  const loadError = ref('')
  const awaitingToken = ref(false)
  const bootstrapping = ref(false)
  const hostContext = ref<Record<string, unknown>>({})

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
    awaitingToken.value = false
    bootstrapping.value = true
    token.value = embedToken

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
      const agentMatches = !stored?.agentId || !configAgentId || stored.agentId === configAgentId
      if (stored && agentMatches && (await isStoredSessionValid(id, apiToken, stored))) {
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
        return
      }
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

  // Discard the current conversation and start a fresh signed session. Backing
  // the "新建对话" affordance — also the privacy escape hatch on shared devices.
  const startNewSession = async () => {
    const id = channelId.value
    const apiToken = token.value
    if (!id || !apiToken) return
    try {
      const sessionRes = await createEmbedSession(id, apiToken)
      const newId = sessionRes?.data?.id || ''
      if (!newId) return
      const agentId = String(config.value?.agent_id || '').trim()
      const next: StoredSession = { id: newId, sig: sessionRes?.data?.sig || '', agentId }
      sessionSig.value = next.sig
      sessionId.value = next.id
      writeStoredSession(id, next)
    } catch {
      // Non-fatal: keep the current session if creating a new one fails.
    }
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
    start()
  })

  onUnmounted(() => {
    removeHostListener?.()
    removeLocaleListener?.()
    removeTokenListener?.()
  })

  return {
    token,
    config,
    sessionId,
    sessionSig,
    loadError,
    awaitingToken,
    bootstrapping,
    hostContext,
    startNewSession,
  }
}
