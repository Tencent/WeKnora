import { onMounted, onUnmounted, ref, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  createEmbedSession,
  exchangeEmbedSession,
  getEmbedConfig,
  onEmbedHostContext,
  onEmbedHostToken,
  parseEmbedTokenFromLocation,
  postEmbedBootstrapRequest,
  postEmbedReady,
  type EmbedChannelPublicConfig,
} from '@/api/embed'

export function useEmbedBridge(channelId: Ref<string>) {
  const { t } = useI18n()
  const route = useRoute()

  const token = ref('')
  const config = ref<EmbedChannelPublicConfig | null>(null)
  const sessionId = ref('')
  const loadError = ref('')
  const awaitingToken = ref(false)
  const hostContext = ref<Record<string, unknown>>({})

  let removeHostListener: (() => void) | null = null
  let removeTokenListener: (() => void) | null = null
  let bootstrapped = false

  const bootstrap = async (embedToken: string) => {
    const id = channelId.value
    if (!id || !embedToken || bootstrapped) return
    bootstrapped = true
    awaitingToken.value = false
    token.value = embedToken

    try {
      let apiToken = embedToken
      try {
        const exchangeRes = await exchangeEmbedSession(id, embedToken)
        if (exchangeRes?.data?.session_token) {
          apiToken = exchangeRes.data.session_token
        }
      } catch {
        // Fall back to publish token when exchange is unavailable.
      }

      const res = await getEmbedConfig(id, apiToken)
      if (!res?.success || !res.data) {
        loadError.value = t('embedPublish.invalidChannel')
        return
      }
      config.value = res.data
      const sessionRes = await createEmbedSession(id, apiToken)
      sessionId.value = sessionRes?.data?.id || ''
      if (!sessionId.value) {
        loadError.value = t('embedPublish.sessionFailed')
        return
      }
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
    }
  }

  const start = async () => {
    removeHostListener = onEmbedHostContext((payload) => {
      hostContext.value = { ...hostContext.value, ...payload }
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
    removeTokenListener?.()
  })

  return {
    token,
    config,
    sessionId,
    loadError,
    awaitingToken,
    hostContext,
  }
}
