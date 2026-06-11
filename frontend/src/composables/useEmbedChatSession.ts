import {
  ref,
  reactive,
  watch,
  nextTick,
  onMounted,
  onUnmounted,
  markRaw,
  type Ref,
} from 'vue'
import { useI18n } from 'vue-i18n'
import { useStream } from '@/api/chat/streame'
import {
  getEmbedMessageList,
  postEmbedMessageSent,
  postEmbedMessageReceived,
} from '@/api/embed'
import { embedToast } from '@/utils/embedToast'

function buildQueryWithHostContext(
  query: string,
  hostContext?: Record<string, unknown>,
): string {
  if (!hostContext || !Object.keys(hostContext).length) return query
  const lines = Object.entries(hostContext)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}: ${typeof v === 'string' ? v : JSON.stringify(v)}`)
  if (!lines.length) return query
  return `[Host context]\n${lines.join('\n')}\n\n${query}`
}

export function useEmbedChatSession(options: {
  sessionId: Ref<string>
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  hostContext?: Ref<Record<string, unknown>>
  onMessagesChange?: (has: boolean) => void
}) {
  const { t } = useI18n()
  const { onChunk, error, startStream, stopStream } = useStream()

  const isAgentStreamSession = () =>
    !!(options.agentId && options.agentId !== 'builtin-quick-answer')

  const ensureAgentMessageShell = (message: Record<string, unknown>, requestId?: string) => {
    message.isAgentMode = true
    if (!message.agentEventStream) message.agentEventStream = []
    if (!message._eventMap) message._eventMap = new Map()
    if (!message._pendingToolCalls) message._pendingToolCalls = new Map()
    if (requestId) {
      if (!message.id) message.id = requestId
      if (!message.request_id) message.request_id = requestId
    }
  }

  const shouldRenderAssistantMessage = (session: { isAgentMode?: boolean; agentEventStream?: unknown[] }) => {
    if (!session?.isAgentMode) return true
    const stream = session.agentEventStream
    return Array.isArray(stream) && stream.length > 0
  }

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
  let fullContent = ''
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

  const findLastMessage = (predicate: (item: Record<string, unknown>) => boolean) => {
    for (let i = messagesList.length - 1; i >= 0; i--) {
      const item = messagesList[i]
      if (predicate(item)) return item
    }
    return undefined
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

  const notifyEmbedReceived = (content: string) => {
    if (!content?.trim()) return
    postEmbedMessageReceived(options.channelId, options.sessionId.value, content)
  }

  const recomposeAgentAnswer = (message: Record<string, unknown>) => {
    const stream = message.agentEventStream as Array<{ type?: string; superseded?: boolean; content?: string }> | undefined
    if (!stream) return ''
    let out = ''
    for (const e of stream) {
      if (e.type === 'answer' && !e.superseded && e.content) {
        out += e.content
      }
    }
    return out
  }

  const reconstructEventStreamFromSteps = (
    agentSteps: unknown[],
    messageContent: string,
    isCompleted = false,
    isFallback = false,
    agentDurationMs = 0,
  ) => {
    const events: Record<string, unknown>[] = []

    if (agentSteps && Array.isArray(agentSteps) && agentSteps.length > 0) {
      agentSteps.forEach((step: Record<string, unknown>) => {
        const stepTimestamp = step.timestamp ? new Date(String(step.timestamp)).getTime() : 0
        const toolCalls = step.tool_calls
        const hasToolCalls = toolCalls && Array.isArray(toolCalls) && toolCalls.length > 0

        const reasoningText =
          step.reasoning_content && String(step.reasoning_content).trim()
            ? String(step.reasoning_content)
            : ''
        if (reasoningText) {
          events.push({
            type: 'thinking',
            event_id: `step-${step.iteration}-thought`,
            content: reasoningText,
            done: true,
            thinking: false,
            timestamp: stepTimestamp || undefined,
            duration_ms: step.duration || undefined,
          })
        }
        const preambleText = step.thought && String(step.thought).trim() ? String(step.thought) : ''
        if (preambleText && hasToolCalls) {
          events.push({
            type: 'answer',
            event_id: `step-${step.iteration}-preamble`,
            content: preambleText,
            done: true,
            superseded: true,
            timestamp: stepTimestamp || undefined,
          })
        }

        if (toolCalls && Array.isArray(toolCalls)) {
          toolCalls.forEach((toolCall: Record<string, unknown>) => {
            if (toolCall.name === 'final_answer') return
            const result = toolCall.result as Record<string, unknown> | undefined
            const resultData = result?.data as Record<string, unknown> | undefined
            events.push({
              type: 'tool_call',
              tool_call_id: toolCall.id,
              tool_name: toolCall.name,
              arguments: toolCall.args,
              pending: false,
              success: result?.success !== false,
              output: result?.output || '',
              error: result?.error || undefined,
              timestamp: stepTimestamp || undefined,
              duration: toolCall.duration,
              duration_ms: toolCall.duration,
              display_type: resultData?.display_type,
              tool_data: result?.data,
            })
          })
        }
      })
    }

    if (agentDurationMs > 0) {
      events.push({
        type: 'agent_complete',
        total_duration_ms: agentDurationMs,
      })
    }

    if (messageContent && messageContent.trim()) {
      const answerEvent: Record<string, unknown> = {
        type: 'answer',
        content: messageContent,
        done: true,
      }
      if (isFallback) answerEvent.is_fallback = true
      events.push(answerEvent)
    } else if (isCompleted) {
      events.push({
        type: 'stop',
        timestamp: Date.now(),
        reason: 'user_requested',
      })
    }

    return events
  }

  const handleMsgList = async (
    data: Record<string, unknown>[],
    isScrollType = false,
    newScrollHeight?: number,
  ) => {
    const chatlist = [...data]
    const existingIds = new Set(messagesList.map((m) => m.id).filter(Boolean))
    const processed: Record<string, unknown>[] = []

    for (const raw of chatlist) {
      let item = { ...raw }
      if (item.id && existingIds.has(item.id)) continue
      if (item.id) existingIds.add(item.id)

      item.isAgentMode = false
      item.agent_steps = item.agent_steps ? markRaw(item.agent_steps) : item.agent_steps
      item.agentEventStream = markRaw((item.agentEventStream as unknown[]) || [])
      item._eventMap = markRaw(new Map())
      item._pendingToolCalls = markRaw(new Map())

      if (item.agent_steps && Array.isArray(item.agent_steps) && item.agent_steps.length > 0) {
        item.isAgentMode = true
        item.agentEventStream = markRaw(
          reconstructEventStreamFromSteps(
            item.agent_steps as unknown[],
            String(item.content || ''),
            Boolean(item.is_completed),
            Boolean(item.is_fallback),
            Number(item.agent_duration_ms) || 0,
          ),
        )
        item.hideContent = true
      }

      if (item.content) {
        const content = String(item.content)
        const thinkCloseTag = '</think>'
        if (!content.includes('<think>') && !content.includes(thinkCloseTag)) {
          item.thinkContent = ''
          item.showThink = false
          item.thinking = false
        } else if (content.includes(thinkCloseTag)) {
          item.showThink = true
          item.thinking = false
          const index = content.trim().lastIndexOf(thinkCloseTag)
          item.thinkContent = content.trim().substring(0, index).replace('<think>', '').trim()
          item.content = content.trim().substring(index + thinkCloseTag.length)
        } else if (content.includes('<think>')) {
          item.showThink = true
          item.thinking = true
          item.thinkContent = content.replace('<think>', '').trim()
          item.content = ''
        }
      }

      processed.push(item)
    }

    if (processed.length > 0) {
      if (isScrollType) {
        for (let i = processed.length - 1; i >= 0; i--) {
          messagesList.unshift(processed[i])
        }
      } else {
        messagesList.push(...processed)
      }
    }

    if (isFirstEnter.value) {
      scrollToBottom(true)
    } else if (isScrollType && scrollContainer.value && typeof newScrollHeight === 'number') {
      nextTick(() => {
        if (!scrollContainer.value) return
        const { scrollHeight } = scrollContainer.value
        scrollContainer.value.scrollTop = scrollHeight - newScrollHeight
      })
    }
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

  const updateAssistantSession = (payload: Record<string, unknown>) => {
    const message = findLastMessage((item) => {
      if (item.request_id === payload.id) return true
      return item.id === payload.id
    })
    if (message) {
      if (payload.id && !message.request_id) message.request_id = payload.id
      message.content = payload.content
      message.thinking = payload.thinking
      message.thinkContent = payload.thinkContent
      message.showThink = payload.showThink
      if (!message.knowledge_references) {
        message.knowledge_references = payload.knowledge_references
      }
      if (payload.is_fallback) message.is_fallback = true
      if (payload.is_completed) message.is_completed = true
    } else {
      const entry = { ...payload }
      if (entry.id && !entry.request_id) entry.request_id = entry.id
      messagesList.push(entry)
    }
    scrollToBottom()
  }

  const handleAgentChunk = (data: Record<string, unknown>) => {
    const dataId = data.id as string | undefined
    let message = findLastMessage(
      (item) => item.request_id === dataId || item.id === dataId,
    )

    if (!message) {
      const newMsg: Record<string, unknown> = {
        id: dataId,
        request_id: dataId,
        role: 'assistant',
        content: '',
        isAgentMode: true,
        agentEventStream: [],
        _eventMap: new Map(),
        knowledge_references: [],
      }
      messagesList.push(newMsg)
      loading.value = false
      scrollToBottom(true)
      message = newMsg
    }

    message.isAgentMode = true

    if (
      loading.value &&
      (data.response_type === 'thinking' ||
        data.response_type === 'answer' ||
        data.response_type === 'tool_call' ||
        data.response_type === 'tool_approval_required')
    ) {
      loading.value = false
    }

    const responseType = data.response_type as string
    const dataPayload = data.data as Record<string, unknown> | undefined

    switch (responseType) {
      case 'thinking': {
        const eventId = dataPayload?.event_id as string | undefined
        if (!message.agentEventStream) message.agentEventStream = []
        if (!message._eventMap) message._eventMap = new Map()
        const eventMap = message._eventMap as Map<string, Record<string, unknown>>
        const stream = message.agentEventStream as Record<string, unknown>[]

        if (!data.done) {
          let thinkingEvent = eventMap.get(eventId || '')
          if (!thinkingEvent) {
            thinkingEvent = {
              type: 'thinking',
              event_id: eventId,
              content: '',
              done: false,
              startTime: Date.now(),
              thinking: true,
            }
            stream.push(thinkingEvent)
            if (eventId) eventMap.set(eventId, thinkingEvent)
          }
          if (data.content) {
            thinkingEvent.content = String(thinkingEvent.content || '') + String(data.content)
          }
        } else {
          const thinkingEvent = eventMap.get(eventId || '')
          if (thinkingEvent) {
            thinkingEvent.done = true
            thinkingEvent.thinking = false
            thinkingEvent.duration_ms =
              dataPayload?.duration_ms || Date.now() - Number(thinkingEvent.startTime || Date.now())
            thinkingEvent.completed_at = dataPayload?.completed_at || Date.now()
          }
        }
        break
      }
      case 'tool_approval_required': {
        if (!message.agentEventStream) message.agentEventStream = []
        const d = dataPayload || {}
        ;(message.agentEventStream as Record<string, unknown>[]).push({
          type: 'tool_approval_required',
          pending_id: d.pending_id,
          service_name: d.service_name,
          mcp_tool_name: d.mcp_tool_name,
          description: d.description,
          args_json: d.args_json,
          timeout_seconds: d.timeout_seconds,
          requested_at: d.requested_at,
          tool_call_id: d.tool_call_id,
          resolved: false,
        })
        break
      }
      case 'tool_approval_resolved': {
        const d = dataPayload || {}
        const pid = d.pending_id
        const ev = (message.agentEventStream as Record<string, unknown>[] | undefined)?.find(
          (e) => e.type === 'tool_approval_required' && e.pending_id === pid,
        )
        if (ev) {
          ev.resolved = true
          ev.approved = d.approved
          ev.resolve_reason = d.reason
          ev.timed_out = d.timed_out
          ev.canceled = d.canceled
        }
        break
      }
      case 'tool_call': {
        if (dataPayload?.tool_name === 'final_answer') break
        if (message.agentEventStream) {
          let retracted = false
          for (const ev of message.agentEventStream as Record<string, unknown>[]) {
            if (ev.type === 'answer' && !ev.superseded && ev.content && String(ev.content).trim()) {
              ev.superseded = true
              ev.done = true
              retracted = true
            }
          }
          if (retracted) {
            message.content = recomposeAgentAnswer(message)
            fullContent = String(message.content || '')
          }
        }
        if (dataPayload && (dataPayload.tool_name || dataPayload.tool_call_id)) {
          if (!message.agentEventStream) message.agentEventStream = []
          if (!message._pendingToolCalls) message._pendingToolCalls = new Map()
          const pending = message._pendingToolCalls as Map<string, Record<string, unknown>>
          const stream = message.agentEventStream as Record<string, unknown>[]
          const incomingToolName = dataPayload.tool_name as string | undefined
          const incomingArguments = dataPayload.arguments
          const toolCallId =
            (dataPayload.tool_call_id as string) ||
            (incomingToolName ? `${incomingToolName}_${Date.now()}` : null)
          if (!toolCallId) break

          let toolCallEvent = pending.get(toolCallId)
          if (!toolCallEvent) {
            toolCallEvent = stream.find(
              (event) => event.type === 'tool_call' && event.tool_call_id === toolCallId,
            )
          }
          if (toolCallEvent) {
            if (incomingToolName) toolCallEvent.tool_name = incomingToolName
            if (incomingArguments) toolCallEvent.arguments = incomingArguments
            toolCallEvent.pending = true
            if (!toolCallEvent.timestamp) toolCallEvent.timestamp = Date.now()
            pending.set(toolCallId, toolCallEvent)
          } else {
            const newToolCallEvent = {
              type: 'tool_call',
              tool_call_id: toolCallId,
              tool_name: incomingToolName,
              arguments: incomingArguments,
              timestamp: Date.now(),
              pending: true,
            }
            stream.push(newToolCallEvent)
            pending.set(toolCallId, newToolCallEvent)
          }
        }
        break
      }
      case 'tool_result':
      case 'error': {
        if (dataPayload) {
          const toolCallId = dataPayload.tool_call_id as string | undefined
          const toolName = dataPayload.tool_name as string | undefined
          const success = responseType !== 'error' && dataPayload.success !== false
          let toolCallEvent: Record<string, unknown> | undefined
          const pending = message._pendingToolCalls as Map<string, Record<string, unknown>> | undefined
          if (pending) {
            if (toolCallId && pending.has(toolCallId)) {
              toolCallEvent = pending.get(toolCallId)
              pending.delete(toolCallId)
            } else {
              for (const [key, value] of pending.entries()) {
                if (value.tool_name === toolName) {
                  toolCallEvent = value
                  pending.delete(key)
                  break
                }
              }
            }
          }
          if (toolCallEvent) {
            toolCallEvent.pending = false
            toolCallEvent.success = success
            toolCallEvent.output = success
              ? dataPayload.output || data.content
              : dataPayload.error || data.content
            toolCallEvent.error = !success ? dataPayload.error || data.content : undefined
            const duration =
              dataPayload.duration_ms !== undefined ? dataPayload.duration_ms : dataPayload.duration
            toolCallEvent.duration = duration
            toolCallEvent.duration_ms = duration
            toolCallEvent.display_type = dataPayload.display_type
            toolCallEvent.tool_data = dataPayload
          } else if (responseType === 'error' && !toolName) {
            const errorMsg = String(data.content || t('chat.processError'))
            message.content = errorMsg
            isReplying.value = false
            loading.value = false
            embedToast(errorMsg)
          }
        } else if (responseType === 'error') {
          const errorMsg = String(data.content || t('chat.processError'))
          message.content = errorMsg
          isReplying.value = false
          loading.value = false
          embedToast(errorMsg)
        }
        break
      }
      case 'references': {
        if (dataPayload?.references) {
          message.knowledge_references = dataPayload.references
        } else if (data.knowledge_references) {
          message.knowledge_references = data.knowledge_references
        }
        break
      }
      case 'answer': {
        message.thinking = false
        const eventId = dataPayload?.event_id as string | undefined
        if (!message.agentEventStream) message.agentEventStream = []
        if (!message._eventMap) message._eventMap = new Map()
        const eventMap = message._eventMap as Map<string, Record<string, unknown>>
        const stream = message.agentEventStream as Record<string, unknown>[]

        let answerEvent = eventId
          ? eventMap.get(eventId)
          : stream.find((e) => e.type === 'answer' && !e.event_id)
        if (!answerEvent) {
          answerEvent = { type: 'answer', event_id: eventId, content: '', done: false }
          stream.push(answerEvent)
          if (eventId) eventMap.set(eventId, answerEvent)
        }
        if (!answerEvent.content && message.content && String(message.content).trim()) {
          answerEvent.content = message.content
        }
        if (data.content) {
          answerEvent.content = String(answerEvent.content || '') + String(data.content)
          message.content = recomposeAgentAnswer(message)
          fullContent = String(message.content || '')
        }
        if (dataPayload?.is_fallback) {
          answerEvent.is_fallback = true
          message.is_fallback = true
        }
        if (data.done && !answerEvent.done) {
          answerEvent.done = true
          loading.value = false
          isReplying.value = false
          fullContent = ''
          currentAssistantMessageId.value = ''
        }
        break
      }
      case 'complete': {
        loading.value = false
        isReplying.value = false
        message.is_completed = true
        notifyEmbedReceived(String(message.content || ''))
        fullContent = ''
        currentAssistantMessageId.value = ''
        if (message.agentEventStream) {
          ;(message.agentEventStream as Record<string, unknown>[]).push({
            type: 'agent_complete',
            total_duration_ms: dataPayload?.total_duration_ms || 0,
            total_steps: dataPayload?.total_steps || 0,
          })
        }
        break
      }
      case 'stop': {
        if (!message.agentEventStream) message.agentEventStream = []
        ;(message.agentEventStream as Record<string, unknown>[]).push({
          type: 'stop',
          timestamp: Date.now(),
          reason: dataPayload?.reason || 'user_requested',
        })
        isReplying.value = false
        fullContent = ''
        break
      }
    }

    scrollToBottom()
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
    if (data.response_type === 'agent_query') {
      if (data.id) {
        const earlyMsg = findLastMessage(
          (item) => item.role === 'assistant' && !item.is_completed,
        )
        if (earlyMsg) earlyMsg.request_id = data.id
      }
      if (data.assistant_message_id) {
        currentAssistantMessageId.value = data.assistant_message_id
      }
      let existingMessage = findLastMessage(
        (item) => item.id === data.id || item.request_id === data.id,
      )
      if (!existingMessage) {
        existingMessage = {
          id: data.id,
          request_id: data.id,
          role: 'assistant',
          content: '',
          isAgentMode: true,
          is_completed: false,
          agentEventStream: [],
          _eventMap: new Map(),
          _pendingToolCalls: new Map(),
          knowledge_references: [],
        }
        messagesList.push(existingMessage)
        scrollToBottom(true)
      } else {
        ensureAgentMessageShell(existingMessage, data.id)
      }
      return
    }

    const isAgentOnlyResponse =
      data.response_type === 'thinking' ||
      data.response_type === 'tool_call' ||
      data.response_type === 'tool_result' ||
      data.response_type === 'reflection'

    const lastMessage = messagesList[messagesList.length - 1]
    const isCurrentlyAgentMode = lastMessage?.isAgentMode === true
    const targetsActiveAgentRequest =
      isAgentStreamSession() &&
      !!data.id &&
      (data.id === currentAssistantMessageId.value ||
        lastMessage?.request_id === data.id ||
        lastMessage?.id === data.id)
    const isAgentAnswerChunk =
      data.response_type === 'answer' && (isAgentStreamSession() || targetsActiveAgentRequest)
    const isAgentCompleteChunk =
      data.response_type === 'complete' && (isAgentStreamSession() || targetsActiveAgentRequest)

    const shouldHandleAsAgent =
      isAgentOnlyResponse ||
      isCurrentlyAgentMode ||
      isAgentAnswerChunk ||
      isAgentCompleteChunk

    if (data.response_type === 'references') {
      if (isCurrentlyAgentMode) {
        handleAgentChunk(data)
        return
      }
      let existingMessage = findLastMessage(
        (item) => item.request_id === data.id || item.id === data.id,
      )
      if (!existingMessage) {
        existingMessage = {
          id: data.id,
          request_id: data.id,
          role: 'assistant',
          content: '',
          showThink: false,
          thinkContent: '',
          thinking: false,
          is_completed: false,
          knowledge_references: [],
        }
        messagesList.push(existingMessage)
        loading.value = false
        scrollToBottom(true)
      }
      existingMessage.knowledge_references =
        data.knowledge_references || data.data?.references || []
      return
    }

    if (shouldHandleAsAgent) {
      handleAgentChunk(data)
      if (data.response_type === 'stop') {
        loading.value = false
        isReplying.value = false
        currentAssistantMessageId.value = ''
      }
      return
    }

    if (data.response_type === 'stop') {
      const stoppedMessage = findLastMessage((item) => {
        if (item.request_id === data.id) return true
        return item.id === data.id
      })
      if (stoppedMessage) stoppedMessage.is_completed = true
      loading.value = false
      isReplying.value = false
      fullContent = ''
      currentAssistantMessageId.value = ''
      return
    }

    const existingMessage = findLastMessage((item) => {
      if (item.request_id === data.id) return true
      return item.id === data.id
    })
    if (existingMessage?.is_completed && data.done && !data.content) return

    fullContent += data.content || ''
    const obj: Record<string, unknown> = {
      ...data,
      content: '',
      role: 'assistant',
      showThink: false,
      is_completed: false,
    }

    if (data.data?.is_fallback) obj.is_fallback = true

    const thinkCloseTag = '</think>'
    if (fullContent.includes('<think>') && !fullContent.includes(thinkCloseTag)) {
      obj.thinking = true
      obj.showThink = true
      obj.content = ''
      obj.thinkContent = fullContent.replace('<think>', '').trim()
    } else if (fullContent.includes('<think>') && fullContent.includes(thinkCloseTag)) {
      obj.thinking = false
      obj.showThink = true
      const index = fullContent.lastIndexOf(thinkCloseTag)
      obj.thinkContent = fullContent.substring(0, index).replace('<think>', '').trim()
      obj.content = fullContent.substring(index + thinkCloseTag.length).trim()
    } else {
      obj.content = fullContent
    }

    if (!existingMessage) loading.value = false

    if (data.done) {
      obj.is_completed = true
      notifyEmbedReceived(String(obj.content || ''))
      isReplying.value = false
      fullContent = ''
      currentAssistantMessageId.value = ''
    }
    updateAssistantSession(obj)
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
    fullContent = ''
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
