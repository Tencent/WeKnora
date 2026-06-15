import { markRaw, nextTick, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ensureRagPipelineHistoryStream } from '@/utils/rag-pipeline-history'

export type ChatMessage = Record<string, unknown>

export interface UseChatStreamHandlerOptions {
  messagesList: ChatMessage[]
  loading: Ref<boolean>
  isReplying: Ref<boolean>
  currentAssistantMessageId: Ref<string>
  fullContent: Ref<string>
  isAgentStreamSession: () => boolean
  scrollToBottom: (force?: boolean) => void
  onReplyComplete?: (content: string) => void
  onError?: (message: string) => void
  /** Main chat: keep the last incomplete message reactive for continue-stream. */
  preserveIncompleteStreamReactive?: boolean
  isFirstEnter?: Ref<boolean>
  scrollContainer?: Ref<HTMLElement | null>
  onAfterMsgList?: () => void | Promise<void>
  onAgentQuery?: (
    data: ChatMessage,
    existingMessage: ChatMessage | undefined,
    created: boolean,
  ) => void
  onMessageCreated?: (message: ChatMessage) => void
  onMessageUpdated?: (message: ChatMessage, payload?: ChatMessage) => void
  onAgentAnswerDone?: (message: ChatMessage) => void
  onAgentChunkBound?: (message: ChatMessage, created: boolean) => void
  debug?: boolean
}

export function useChatStreamHandler(options: UseChatStreamHandlerOptions) {
  const { t } = useI18n()
  const {
    messagesList,
    loading,
    isReplying,
    currentAssistantMessageId,
    fullContent,
    isAgentStreamSession,
    scrollToBottom,
    onReplyComplete,
    onError,
    preserveIncompleteStreamReactive = false,
    isFirstEnter,
    scrollContainer,
    onAfterMsgList,
    onAgentQuery,
    onMessageCreated,
    onMessageUpdated,
    onAgentAnswerDone,
    onAgentChunkBound,
    debug = false,
  } = options

  const log = (...args: unknown[]) => {
    if (debug) console.log(...args)
  }

  const findLastMessage = (predicate: (item: ChatMessage) => boolean) => {
    for (let i = messagesList.length - 1; i >= 0; i--) {
      const item = messagesList[i]
      if (predicate(item)) return item
    }
    return undefined
  }

  const extractKnowledgeReferences = (data: ChatMessage) => {
    const dataPayload = data.data as ChatMessage | undefined
    const refs =
      data.knowledge_references ||
      dataPayload?.references ||
      dataPayload?.knowledge_references ||
      []
    return Array.isArray(refs) ? refs : []
  }

  /** Match the in-flight assistant row by request id or assistant message id. */
  const resolveActiveAssistantMessage = (data: ChatMessage) => {
    const dataId = data.id as string | undefined
    const assistantId =
      (data.assistant_message_id as string | undefined) ||
      currentAssistantMessageId.value ||
      undefined

    const matched = findLastMessage((item) => {
      if (item.role !== 'assistant') return false
      if (dataId && (item.request_id === dataId || item.id === dataId)) return true
      if (assistantId && (item.id === assistantId || item.request_id === assistantId)) return true
      return false
    })
    if (matched) return matched

    return findLastMessage((item) => item.role === 'assistant' && !item.is_completed)
  }

  const applyKnowledgeReferences = (data: ChatMessage) => {
    const refs = extractKnowledgeReferences(data)
    if (!refs.length) return undefined

    let message = resolveActiveAssistantMessage(data)
    const created = !message
    if (!message) {
      const rowId = (data.id as string | undefined) || currentAssistantMessageId.value
      message = {
        id: rowId,
        request_id: rowId,
        role: 'assistant',
        content: '',
        showThink: false,
        thinkContent: '',
        thinking: false,
        is_completed: false,
        knowledge_references: [],
      }
      ensureAgentMessageShell(message, data.id as string | undefined)
      messagesList.push(message)
      onMessageCreated?.(message)
      loading.value = false
    } else {
      ensureAgentMessageShell(message, data.id as string | undefined)
    }

    message.knowledge_references = refs.slice()
    if (created) onAgentChunkBound?.(message, true)
    onMessageUpdated?.(message, data)
    log('[References] Saved to message, count:', refs.length)
    return message
  }

  const ensureAgentMessageShell = (message: ChatMessage, requestId?: string) => {
    message.isAgentMode = true
    if (!isAgentStreamSession()) {
      message.isRagMode = true
    }
    if (!message.agentEventStream) message.agentEventStream = []
    if (!message._eventMap) message._eventMap = new Map()
    if (!message._pendingToolCalls) message._pendingToolCalls = new Map()
    if (requestId) {
      if (!message.id) message.id = requestId
      if (!message.request_id) message.request_id = requestId
    }
  }

  const shouldRenderAssistantMessage = (session: ChatMessage) => {
    if (!session?.isAgentMode) return true
    if (!session.is_completed) return true
    const stream = session.agentEventStream
    if (Array.isArray(stream) && stream.length > 0) return true
    if (Array.isArray(session.knowledge_references) && session.knowledge_references.length > 0) {
      return true
    }
    return false
  }

  const shouldShowGlobalTypingIndicator = (
    messages: ChatMessage[],
    isLoading: boolean,
    isRecovering = false,
  ) => {
    if (!isLoading && !isRecovering) return false
    const last = messages[messages.length - 1]
    if (last?.role === 'assistant' && last?.isAgentMode && !last?.is_completed) {
      return false
    }
    return true
  }

  /** Quick-answer sessions: restore flags lost after history reload. */
  const restoreQuickAnswerFlags = (item: ChatMessage) => {
    if (isAgentStreamSession() || item.role !== 'assistant') return
    item.isRagMode = true
    if (
      item.agent_steps &&
      Array.isArray(item.agent_steps) &&
      item.agent_steps.length > 0
    ) {
      item.isAgentMode = true
      item.hideContent = true
    }
    ensureRagPipelineHistoryStream(item as Parameters<typeof ensureRagPipelineHistoryStream>[0])
    if (item.isRagMode && item.agentEventStream) {
      item.agentEventStream = markRaw(item.agentEventStream as object)
    }
  }

  const recomposeAgentAnswer = (message: ChatMessage) => {
    const stream = message.agentEventStream as Array<{
      type?: string
      superseded?: boolean
      content?: string
    }> | undefined
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
    const events: ChatMessage[] = []

    if (agentSteps && Array.isArray(agentSteps) && agentSteps.length > 0) {
      agentSteps.forEach((step: ChatMessage) => {
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
          toolCalls.forEach((toolCall: ChatMessage) => {
            if (toolCall.name === 'final_answer') return
            const result = toolCall.result as ChatMessage | undefined
            const resultData = result?.data as ChatMessage | undefined
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
      const answerEvent: ChatMessage = {
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
    data: ChatMessage[],
    isScrollType = false,
    newScrollHeight?: number,
  ) => {
    const chatlist = [...data]
    const existingIds = new Set(messagesList.map((m) => m.id).filter(Boolean))
    const processed: ChatMessage[] = []

    for (const raw of chatlist) {
      const item = preserveIncompleteStreamReactive ? raw : { ...raw }
      if (item.id && existingIds.has(item.id)) continue
      if (item.id) existingIds.add(item.id)

      item.isAgentMode = false
      const willContinueStream = preserveIncompleteStreamReactive && !item.is_completed
      if (willContinueStream) {
        item.agentEventStream = item.agentEventStream || []
        item._eventMap = new Map()
        item._pendingToolCalls = new Map()
      } else {
        item.agent_steps = item.agent_steps ? markRaw(item.agent_steps as object) : item.agent_steps
        item.agentEventStream = markRaw((item.agentEventStream as unknown[]) || [])
        item._eventMap = markRaw(new Map())
        item._pendingToolCalls = markRaw(new Map())
      }

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

      restoreQuickAnswerFlags(item)

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

    if (isFirstEnter?.value) {
      scrollToBottom(true)
    } else if (isScrollType && scrollContainer?.value && typeof newScrollHeight === 'number') {
      nextTick(() => {
        if (!scrollContainer.value) return
        const { scrollHeight } = scrollContainer.value
        scrollContainer.value.scrollTop = scrollHeight - newScrollHeight
      })
    }

    if (onAfterMsgList) {
      await onAfterMsgList()
    }
  }

  const updateAssistantSession = (payload: ChatMessage) => {
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
      onMessageUpdated?.(message, payload)
    } else {
      const entry = { ...payload }
      if (entry.id && !entry.request_id) entry.request_id = entry.id
      messagesList.push(entry)
      onMessageCreated?.(entry)
      onMessageUpdated?.(entry, payload)
    }
    scrollToBottom()
  }

  const reportError = (errorMsg: string) => {
    if (onError) {
      onError(errorMsg)
    }
  }

  const handleAgentChunk = (data: ChatMessage) => {
    const dataId = data.id as string | undefined
    let message = findLastMessage(
      (item) => item.request_id === dataId || item.id === dataId,
    )
    let created = false

    if (!message) {
      const newMsg: ChatMessage = {
        id: dataId,
        request_id: dataId,
        role: 'assistant',
        content: '',
        isAgentMode: true,
        isRagMode: !isAgentStreamSession(),
        agentEventStream: [],
        _eventMap: new Map(),
        knowledge_references: [],
      }
      messagesList.push(newMsg)
      onMessageCreated?.(newMsg)
      loading.value = false
      scrollToBottom(true)
      message = newMsg
      created = true
    } else {
      onAgentChunkBound?.(message, false)
    }

    if (created) {
      onAgentChunkBound?.(message, true)
    }

    message.isAgentMode = true

    if (
      loading.value &&
      (data.response_type === 'thinking' ||
        data.response_type === 'answer' ||
        data.response_type === 'tool_call' ||
        data.response_type === 'tool_approval_required')
    ) {
      log('[Agent Chunk] Closing loading for continued stream')
      loading.value = false
    }

    const responseType = data.response_type as string
    const dataPayload = data.data as ChatMessage | undefined

    switch (responseType) {
      case 'thinking': {
        const eventId = dataPayload?.event_id as string | undefined
        log('[Thinking Event]', {
          event_id: eventId,
          done: data.done,
          content_length: (data.content as string | undefined)?.length || 0,
        })
        if (!message.agentEventStream) message.agentEventStream = []
        if (!message._eventMap) message._eventMap = new Map()
        const eventMap = message._eventMap as Map<string, ChatMessage>
        const stream = message.agentEventStream as ChatMessage[]

        if (!data.done) {
          let thinkingEvent = eventMap.get(eventId || '')
          if (!thinkingEvent) {
            log('[Thinking] Creating new thinking event, event_id:', eventId)
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
            log('[Thinking] Event', eventId, 'accumulated:', String(thinkingEvent.content).length, 'chars')
          }
        } else {
          const thinkingEvent = eventMap.get(eventId || '')
          if (thinkingEvent) {
            thinkingEvent.done = true
            thinkingEvent.thinking = false
            thinkingEvent.duration_ms =
              dataPayload?.duration_ms || Date.now() - Number(thinkingEvent.startTime || Date.now())
            thinkingEvent.completed_at = dataPayload?.completed_at || Date.now()
            log('[Thinking] Event completed, duration:', thinkingEvent.duration_ms, 'ms')
          } else {
            console.warn('[Thinking] Received done for unknown event_id:', eventId)
          }
        }
        break
      }
      case 'tool_approval_required': {
        if (!message.agentEventStream) message.agentEventStream = []
        const d = dataPayload || {}
        ;(message.agentEventStream as ChatMessage[]).push({
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
        const ev = (message.agentEventStream as ChatMessage[] | undefined)?.find(
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
          for (const ev of message.agentEventStream as ChatMessage[]) {
            if (ev.type === 'answer' && !ev.superseded && ev.content && String(ev.content).trim()) {
              ev.superseded = true
              ev.done = true
              retracted = true
            }
          }
          if (retracted) {
            message.content = recomposeAgentAnswer(message)
            fullContent.value = String(message.content || '')
          }
        }
        if (dataPayload && (dataPayload.tool_name || dataPayload.tool_call_id)) {
          if (!message.agentEventStream) message.agentEventStream = []
          if (!message._pendingToolCalls) message._pendingToolCalls = new Map()
          const pending = message._pendingToolCalls as Map<string, ChatMessage>
          const stream = message.agentEventStream as ChatMessage[]
          const incomingToolName = dataPayload.tool_name as string | undefined
          const incomingArguments = dataPayload.arguments
          const toolCallId =
            (dataPayload.tool_call_id as string) ||
            (incomingToolName ? `${incomingToolName}_${Date.now()}` : null)
          if (!toolCallId) {
            console.warn('[Tool Call] Received event without identifiable tool_call_id:', dataPayload)
            break
          }

          log('[Tool Call]', {
            tool_call_id: toolCallId,
            tool_name: incomingToolName,
            has_arguments: Boolean(incomingArguments),
          })

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
          log('[Tool Result]', {
            tool_call_id: toolCallId,
            tool_name: toolName,
            success,
          })
          let toolCallEvent: ChatMessage | undefined
          const pending = message._pendingToolCalls as Map<string, ChatMessage> | undefined
          if (pending) {
            if (toolCallId && pending.has(toolCallId)) {
              toolCallEvent = pending.get(toolCallId)
              pending.delete(toolCallId)
            } else {
              Array.from(pending.entries()).some(([key, value]) => {
                if (value.tool_name === toolName) {
                  toolCallEvent = value
                  pending.delete(key)
                  return true
                }
                return false
              })
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
            log('[Tool Result] Updated event in stream')
          } else {
            console.warn('[Tool Result] No pending tool call found for', toolCallId || toolName)
          }
          if (responseType === 'error' && !toolName) {
            const errorMsg = String(data.content || t('chat.processError'))
            message.content = errorMsg
            message.is_completed = true
            isReplying.value = false
            loading.value = false
            fullContent.value = ''
            currentAssistantMessageId.value = ''
            reportError(errorMsg)
            console.error('[Chat Error]', errorMsg)
          }
        } else if (responseType === 'error') {
          const errorMsg = String(data.content || t('chat.processError'))
          message.content = errorMsg
          message.is_completed = true
          isReplying.value = false
          loading.value = false
          fullContent.value = ''
          currentAssistantMessageId.value = ''
          reportError(errorMsg)
          console.error('[Chat Error]', errorMsg)
        }
        break
      }
      case 'answer': {
        message.thinking = false
        const eventId = dataPayload?.event_id as string | undefined
        if (!message.agentEventStream) message.agentEventStream = []
        if (!message._eventMap) message._eventMap = new Map()
        const eventMap = message._eventMap as Map<string, ChatMessage>
        const stream = message.agentEventStream as ChatMessage[]

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
          fullContent.value = String(message.content || '')
        }
        if (dataPayload?.is_fallback) {
          answerEvent.is_fallback = true
          message.is_fallback = true
        }
        if (data.done && !answerEvent.done) {
          answerEvent.done = true
          onAgentAnswerDone?.(message)
          loading.value = false
          isReplying.value = false
          fullContent.value = ''
          currentAssistantMessageId.value = ''
        }
        break
      }
      case 'complete': {
        log('[Agent] Complete event received')
        loading.value = false
        isReplying.value = false
        message.is_completed = true
        onReplyComplete?.(String(message.content || ''))
        fullContent.value = ''
        currentAssistantMessageId.value = ''
        if (message.agentEventStream) {
          ;(message.agentEventStream as ChatMessage[]).push({
            type: 'agent_complete',
            total_duration_ms: dataPayload?.total_duration_ms || 0,
            total_steps: dataPayload?.total_steps || 0,
          })
        }
        break
      }
      case 'stop': {
        log('[Agent] Stop event received')
        if (!message.agentEventStream) message.agentEventStream = []
        ;(message.agentEventStream as ChatMessage[]).push({
          type: 'stop',
          timestamp: Date.now(),
          reason: dataPayload?.reason || 'user_requested',
        })
        isReplying.value = false
        fullContent.value = ''
        break
      }
    }

    scrollToBottom()
  }

  const processStreamChunk = (data: ChatMessage) => {
    log('[Agent Event Received]', {
      response_type: data.response_type,
      id: data.id,
      done: data.done,
      content_length: (data.content as string | undefined)?.length || 0,
      content_preview: data.content ? String(data.content).substring(0, 50) : '',
      data: data.data,
      session_id: data.session_id,
      assistant_message_id: data.assistant_message_id,
    })

    if (data.response_type === 'agent_query') {
      if (data.id) {
        const earlyMsg = findLastMessage(
          (item) => item.role === 'assistant' && !item.is_completed,
        )
        if (earlyMsg) earlyMsg.request_id = data.id
      }
      if (data.assistant_message_id) {
        currentAssistantMessageId.value = data.assistant_message_id as string
        log('[Agent Query] Saved assistant message ID:', data.assistant_message_id)
      }
      log('[Agent Query Event]', {
        session_id: data.session_id || (data.data as ChatMessage | undefined)?.session_id,
        assistant_message_id: data.assistant_message_id,
        query: (data.data as ChatMessage | undefined)?.query,
        request_id: (data.data as ChatMessage | undefined)?.request_id,
      })

      let existingMessage = findLastMessage(
        (item) => item.id === data.id || item.request_id === data.id,
      )
      const created = !existingMessage
      if (!existingMessage) {
        existingMessage = {
          id: data.id,
          request_id: data.id,
          role: 'assistant',
          content: '',
          isAgentMode: true,
          isRagMode: !isAgentStreamSession(),
          is_completed: false,
          agentEventStream: [],
          _eventMap: new Map(),
          _pendingToolCalls: new Map(),
          knowledge_references: [],
        }
        messagesList.push(existingMessage)
        onMessageCreated?.(existingMessage)
        loading.value = false
        scrollToBottom(true)
        log('[Agent Query] Created agent placeholder message')
      } else {
        ensureAgentMessageShell(existingMessage, data.id as string | undefined)
        log('[Agent Query] Continuing stream for existing message')
      }
      onAgentQuery?.(data, existingMessage, created)
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
      applyKnowledgeReferences(data)
      scrollToBottom()
      return
    }

    if (shouldHandleAsAgent) {
      handleAgentChunk(data)
      if (data.response_type === 'stop') {
        log('[Stop Event] Generation stopped')
        loading.value = false
        isReplying.value = false
        currentAssistantMessageId.value = ''
      }
      return
    }

    if (data.response_type === 'stop') {
      log('[Stop Event] Non-agent generation stopped')
      const stoppedMessage = findLastMessage((item) => {
        if (item.request_id === data.id) return true
        return item.id === data.id
      })
      if (stoppedMessage) stoppedMessage.is_completed = true
      loading.value = false
      isReplying.value = false
      fullContent.value = ''
      currentAssistantMessageId.value = ''
      return
    }

    const existingMessage = findLastMessage((item) => {
      if (item.request_id === data.id) return true
      return item.id === data.id
    })
    if (existingMessage?.is_completed && data.done && !data.content) {
      log('[Non-Agent] Ignoring duplicate completion event for completed message')
      return
    }

    fullContent.value += (data.content as string) || ''
    const obj: ChatMessage = {
      ...data,
      content: '',
      role: 'assistant',
      showThink: false,
      is_completed: false,
    }

    if ((data.data as ChatMessage | undefined)?.is_fallback) obj.is_fallback = true

    const thinkCloseTag = '</think>'
    if (fullContent.value.includes('<think>') && !fullContent.value.includes(thinkCloseTag)) {
      obj.thinking = true
      obj.showThink = true
      obj.content = ''
      obj.thinkContent = fullContent.value.replace('<think>', '').trim()
    } else if (fullContent.value.includes('<think>') && fullContent.value.includes(thinkCloseTag)) {
      obj.thinking = false
      obj.showThink = true
      const index = fullContent.value.lastIndexOf(thinkCloseTag)
      obj.thinkContent = fullContent.value.substring(0, index).replace('<think>', '').trim()
      obj.content = fullContent.value.substring(index + thinkCloseTag.length).trim()
    } else {
      obj.content = fullContent.value
    }

    if (!existingMessage) loading.value = false

    if (data.done) {
      obj.is_completed = true
      onReplyComplete?.(String(obj.content || ''))
      isReplying.value = false
      fullContent.value = ''
      currentAssistantMessageId.value = ''
    }
    updateAssistantSession(obj)
  }

  return {
    findLastMessage,
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    handleMsgList,
    processStreamChunk,
  }
}
