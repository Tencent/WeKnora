import {
  ref,
  reactive,
  watch,
  nextTick,
  onMounted,
  onUnmounted,
  type Ref,
} from 'vue'
import { useStream } from '@/api/chat/streame'
import {
  getEmbedMessageList,
  postEmbedMessageSent,
  postEmbedMessageReceived,
  relayEmbedWebhookEvent,
  stopEmbedSession,
} from '@/api/embed'
import { embedToast } from '@/utils/embedToast'
import { buildQueryWithHostContext } from '@/utils/embedContext'
import { fileToDataURI } from '@/utils/embedFile'
import { useI18n } from 'vue-i18n'
import { useChatStreamHandler } from '@/composables/useChatStreamHandler'
import { useStickyBottomOnResize } from '@/composables/useStickyBottomOnResize'

export function useEmbedChatSession(options: {
  sessionId: Ref<string>
  sessionSig: Ref<string>
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  allowWebSearch?: boolean
  allowFileUpload?: boolean
  hostContext?: Ref<Record<string, unknown>>
  onMessagesChange?: (has: boolean) => void
  onSessionTitle?: (title: string) => void
}) {
  const { t } = useI18n()
  const { onChunk, error, startStream, stopStream } = useStream()

  const isAgentStreamSession = () =>
    !!(options.agentId && options.agentId !== 'builtin-quick-answer')

  const limit = ref(20)
  const messagesList = reactive<Record<string, unknown>[]>([])
  watch(
    () => messagesList.length,
    (len) => options.onMessagesChange?.(len > 0),
    { immediate: true },
  )

  const isReplying = ref(false)
  const currentAssistantMessageId = ref('')
  const isFirstEnter = ref(true)
  const loading = ref(false)
  const historyLoading = ref(true)
  const historyLoadingMore = ref(false)
  const hasMoreHistory = ref(true)
  const created_at = ref('')
  const fullContent = ref('')
  const scrollContainer = ref<HTMLElement | null>(null)
  const userHasScrolledUp = ref(false)
  const SCROLL_BOTTOM_THRESHOLD = 80

  const isNearBottom = () => {
    if (!scrollContainer.value) return true
    const { scrollTop, scrollHeight, clientHeight } = scrollContainer.value
    return scrollHeight - scrollTop - clientHeight < SCROLL_BOTTOM_THRESHOLD
  }

  const getUserQuery = (index: number) => {
    if (index <= 0) return ''
    const previous = messagesList[index - 1]
    if (previous && previous.role === 'user') {
      return String(previous.content || '')
    }
    return ''
  }

  const scrollToBottom = (force = false) => {
    if (!force && userHasScrolledUp.value) return
    nextTick(() => {
      if (scrollContainer.value) {
        scrollContainer.value.scrollTop = scrollContainer.value.scrollHeight
      }
    })
  }

  const onClickScrollToBottom = () => {
    userHasScrolledUp.value = false
    scrollToBottom(true)
  }

  useStickyBottomOnResize(scrollContainer, userHasScrolledUp, scrollToBottom)

  const debounce = <T extends (...args: never[]) => void>(fn: T, delay: number) => {
    let timer: ReturnType<typeof setTimeout>
    return (...args: Parameters<T>) => {
      clearTimeout(timer)
      timer = setTimeout(() => fn(...args), delay)
    }
  }

  const notifyEmbedReceived = (content: string) => {
    if (!content?.trim()) return
    postEmbedMessageReceived(options.channelId, options.sessionId.value, content)
    relayEmbedWebhookEvent(
      options.channelId,
      options.token,
      options.sessionId.value,
      options.sessionSig.value,
      { type: 'message_received', content },
    )
  }

  const {
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    handleMsgList,
    processStreamChunk,
    prepareForNewOutgoingMessage,
    markInFlightAssistantStopped,
  } = useChatStreamHandler({
    messagesList,
    loading,
    isReplying,
    currentAssistantMessageId,
    fullContent,
    isAgentStreamSession,
    scrollToBottom,
    onReplyComplete: notifyEmbedReceived,
    onError: embedToast,
    isFirstEnter,
    scrollContainer,
  })

  const onChatScrollTop = () => {
    if (historyLoadingMore.value || !hasMoreHistory.value) return
    if (!scrollContainer.value) return
    const { scrollTop, scrollHeight } = scrollContainer.value
    isFirstEnter.value = false
    if (scrollTop <= 0) {
      getmsgList(
        {
          session_id: options.sessionId.value,
          created_at: created_at.value,
          limit: limit.value,
        },
        true,
        scrollHeight,
      )
    }
  }

  const debouncedScrollTop = debounce(onChatScrollTop, 500)

  let lastScrollTop = 0
  const handleScroll = () => {
    const el = scrollContainer.value
    if (el) {
      const currentTop = el.scrollTop
      // Only an actual upward scroll detaches from the live edge. Content that
      // grows after a chunk (images, diagrams) keeps scrollTop fixed and would
      // otherwise fire a stale scroll event that falsely marks the user as
      // scrolled up, killing the auto-follow during streaming.
      if (currentTop < lastScrollTop - 1) {
        userHasScrolledUp.value = !isNearBottom()
      } else if (isNearBottom()) {
        userHasScrolledUp.value = false
      }
      lastScrollTop = currentTop
    }
    debouncedScrollTop()
  }

  const getmsgList = (
    data: { session_id: string; created_at?: string; limit: number },
    isScrollType = false,
    scrollHeight?: number,
  ) => {
    if (isScrollType) {
      if (historyLoadingMore.value || !hasMoreHistory.value) return
      historyLoadingMore.value = true
    }

    getEmbedMessageList(
      options.channelId,
      options.token,
      data.session_id,
      data.limit,
      data.created_at || undefined,
      options.sessionSig.value,
    )
      .then(async (res) => {
        const batch = res?.data as Record<string, unknown>[] | undefined
        if (!batch?.length) {
          // No (more) server history. Crucially this also covers the initial
          // load of a brand-new session: leaving hasMoreHistory true here would
          // let a later scroll-to-top re-fetch with an empty cursor (= "latest"),
          // pulling back the just-sent messages and duplicating them.
          hasMoreHistory.value = false
          return
        }
        const nextCursor = String(batch[0].created_at)
        if (isScrollType && created_at.value && nextCursor === created_at.value) {
          hasMoreHistory.value = false
          return
        }
        if (batch.length < limit.value) hasMoreHistory.value = false
        created_at.value = nextCursor
        await handleMsgList(batch, isScrollType, scrollHeight)
      })
      .catch((err) => {
        console.error('Failed to load messages:', err)
        if (isScrollType) hasMoreHistory.value = false
      })
      .finally(() => {
        historyLoading.value = false
        historyLoadingMore.value = false
      })
  }

  const handleStopGeneration = () => {
    stopStream()
    markInFlightAssistantStopped(currentAssistantMessageId.value)
    const messageId = currentAssistantMessageId.value
    if (messageId) {
      stopEmbedSession(
        options.channelId,
        options.token,
        options.sessionId.value,
        messageId,
        options.sessionSig.value,
      ).catch((err) => console.error('Failed to stop embed generation:', err))
    }
    loading.value = false
    isReplying.value = false
  }

  const sendMsg = async (
    value: string,
    opts: { webSearchEnabled?: boolean; imageFiles?: File[]; attachmentFiles?: File[] } = {},
  ) => {
    stopStream()
    prepareForNewOutgoingMessage()
    const outboundQuery = buildQueryWithHostContext(value, options.hostContext?.value)
    const visitorWebSearchEnabled = opts.webSearchEnabled ?? false
    const imageFiles = (options.allowFileUpload ? opts.imageFiles : undefined) || []
    const attachmentFiles = (options.allowFileUpload ? opts.attachmentFiles : undefined) || []
    isReplying.value = true
    loading.value = true

    const imageAttachments: Array<{ data: string }> = []
    const displayImages: Array<{ url: string }> = []
    const attachmentUploads: Array<{ data: string; file_name: string; file_size: number }> = []
    const displayAttachments: Array<{ file_name: string; file_size: number }> = []
    try {
      for (const file of imageFiles) {
        const dataURI = String(await fileToDataURI(file))
        imageAttachments.push({ data: dataURI })
        displayImages.push({ url: dataURI })
      }
      for (const file of attachmentFiles) {
        const dataURI = String(await fileToDataURI(file))
        attachmentUploads.push({ data: dataURI, file_name: file.name, file_size: file.size })
        displayAttachments.push({ file_name: file.name, file_size: file.size })
      }
    } catch (err) {
      console.error('Failed to read attachment:', err)
      embedToast(t('chat.imageReadFailed'))
      isReplying.value = false
      loading.value = false
      return
    }

    messagesList.push({
      content: value,
      role: 'user',
      mentioned_items: [],
      images: displayImages,
      attachments: displayAttachments,
      channel: 'embed',
    })
    postEmbedMessageSent(options.channelId, options.sessionId.value, value)
    relayEmbedWebhookEvent(
      options.channelId,
      options.token,
      options.sessionId.value,
      options.sessionSig.value,
      { type: 'message_sent', query: value },
    )
    userHasScrolledUp.value = false
    scrollToBottom(true)

    const agentEnabled = isAgentStreamSession()
    const endpoint = agentEnabled
      ? `/api/v1/embed/${options.channelId}/agent-chat`
      : `/api/v1/embed/${options.channelId}/knowledge-chat`

    await startStream({
      session_id: options.sessionId.value,
      knowledge_base_ids: options.kbIds,
      knowledge_ids: [],
      agent_enabled: agentEnabled,
      agent_id: options.agentId,
      web_search_enabled: (options.allowWebSearch ?? false) && visitorWebSearchEnabled,
      enable_memory: false,
      summary_model_id: '',
      mcp_service_ids: [],
      mentioned_items: [],
      images: imageAttachments.length > 0 ? imageAttachments : undefined,
      attachment_uploads: attachmentUploads.length > 0 ? attachmentUploads : undefined,
      query: outboundQuery,
      method: 'POST',
      url: endpoint,
      embed_token: options.token,
      embed_session_sig: options.sessionSig.value,
    })
  }

  watch(error, (newError) => {
    if (newError) {
      embedToast(newError)
      isReplying.value = false
      loading.value = false
      currentAssistantMessageId.value = ''
    }
  })

  onChunk((data) => {
    if (data.response_type === 'session_title') {
      const title = String(data.content || (data.data as { title?: string })?.title || '').trim()
      if (title) {
        options.onSessionTitle?.(title)
      }
      return
    }
    processStreamChunk(data)
  })

  const resetAndLoad = (sid: string) => {
    messagesList.splice(0)
    historyLoading.value = true
    historyLoadingMore.value = false
    hasMoreHistory.value = true
    created_at.value = ''
    loading.value = false
    isReplying.value = false
    currentAssistantMessageId.value = ''
    userHasScrolledUp.value = false
    isFirstEnter.value = true
    fullContent.value = ''
    if (!sid) {
      historyLoading.value = false
      return
    }
    getmsgList({ session_id: sid, created_at: '', limit: limit.value })
  }

  watch(
    () => options.sessionId.value,
    (sid) => resetAndLoad(sid),
    { immediate: true },
  )

  onMounted(() => {
    loading.value = false
    isReplying.value = false
  })

  onUnmounted(() => {
    stopStream()
    fullContent.value = ''
  })

  return {
    messagesList,
    loading,
    isReplying,
    historyLoading,
    scrollContainer,
    userHasScrolledUp,
    isFirstEnter,
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    getUserQuery,
    handleScroll,
    scrollToBottom,
    onClickScrollToBottom,
    sendMsg,
    handleStopGeneration,
  }
}
