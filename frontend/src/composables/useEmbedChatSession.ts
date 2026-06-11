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
} from '@/api/embed'
import { embedToast } from '@/utils/embedToast'
import { buildQueryWithHostContext } from '@/utils/embedContext'
import { useChatStreamHandler } from '@/composables/useChatStreamHandler'

export function useEmbedChatSession(options: {
  sessionId: Ref<string>
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  hostContext?: Ref<Record<string, unknown>>
  onMessagesChange?: (has: boolean) => void
}) {
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
  }

  const {
    shouldRenderAssistantMessage,
    handleMsgList,
    processStreamChunk,
  } = useChatStreamHandler({
    messagesList,
    sessionId: options.sessionId,
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

  const handleScroll = () => {
    userHasScrolledUp.value = !isNearBottom()
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
    )
      .then(async (res) => {
        const batch = res?.data as Record<string, unknown>[] | undefined
        if (!batch?.length) {
          if (isScrollType) hasMoreHistory.value = false
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
    loading.value = false
    isReplying.value = false
    stopStream()
  }

  const sendMsg = async (value: string) => {
    const outboundQuery = buildQueryWithHostContext(value, options.hostContext?.value)
    isReplying.value = true
    loading.value = true

    messagesList.push({
      content: value,
      role: 'user',
      mentioned_items: [],
      images: [],
      attachments: [],
      channel: 'web',
    })
    postEmbedMessageSent(options.channelId, options.sessionId.value, value)
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
      web_search_enabled: false,
      enable_memory: false,
      summary_model_id: '',
      mcp_service_ids: [],
      mentioned_items: [],
      query: outboundQuery,
      method: 'POST',
      url: endpoint,
      embed_token: options.token,
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
    getUserQuery,
    handleScroll,
    scrollToBottom,
    onClickScrollToBottom,
    sendMsg,
    handleStopGeneration,
  }
}
