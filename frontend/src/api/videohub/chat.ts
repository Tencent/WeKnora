import { fetchEventSource } from '@microsoft/fetch-event-source'
import { get, post } from '@/utils/request'
import { getApiBaseUrl } from '@/utils/api-base'
import type { ChatMessage, ChatSession, EvidenceLink, VideoData } from '@/types/videohub'
import { displayQuestionFromStoredContent, mergeLocalTurnWithStoredMessages, parseWeKnoraStreamChunk } from './chatStream'

type ChatScope = 'global' | 'video'

interface ScopeResponse {
  scope: ChatScope
  video_id?: string
  video_title?: string
  video_cover_url?: string
  agent_id?: string
  knowledge_base_ids: string[]
  knowledge_ids: string[]
  session_meta: Record<string, string>
}

interface WeKnoraSession {
  id: string
  title?: string
  description?: string
  created_at?: string
  updated_at?: string
}

interface WeKnoraMessage {
  id: string
  content: string
  role: 'user' | 'assistant' | 'system'
  created_at?: string
  updated_at?: string
  knowledge_references?: KnowledgeReference[]
  is_completed?: boolean
}

interface KnowledgeReference {
  knowledge_id?: string
  knowledge_title?: string
  content?: string
  metadata?: Record<string, string>
}

interface EvidenceLookupItem {
  knowledge_id: string
  video_id: string
  video_title: string
  video_cover_url: string
  seconds: number
  timestamp: string
}

interface ApiEnvelope<T> {
  data?: T
  success?: boolean
  total?: number
}

interface SendOptions {
  currentVideo?: VideoData
  currentTime?: number
  globalMode?: boolean
  onMessage?: (message: ChatMessage) => void
  onStreamMessage?: (message: StreamingChatMessage) => void
}

interface TurnOptions extends SendOptions {
  session?: ChatSession
}

interface StreamAnswerOptions {
  onChunk?: (message: StreamingChatMessage) => void
}

export interface StreamingChatMessage extends ChatMessage {
  thinkingText?: string
  activityText?: string
  role?: 'assistant' | 'user'
  content?: string
  isAgentMode?: boolean
  is_completed?: boolean
  request_id?: string
  assistant_message_id?: string
  agentEventStream?: Record<string, unknown>[]
}

interface StreamingAnswerResult {
  answer: string
  streamMessage: StreamingChatMessage
}

interface WeKnoraStreamPayload {
  response_type?: string
  type?: string
  id?: string
  assistant_message_id?: string
  content?: string
  data?: Record<string, unknown>
  done?: boolean
}

const VIDEOHUB_META_PREFIX = 'videohub:'

function messageId() {
  return `message-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
}

function nowLabel() {
  return new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

function formatTime(seconds: number) {
  const safe = Math.max(0, Math.floor(seconds))
  return `${String(Math.floor(safe / 60)).padStart(2, '0')}:${String(safe % 60).padStart(2, '0')}`
}

function formatRelativeTime(input?: string) {
  if (!input) return '最近'
  const value = new Date(input).getTime()
  if (!Number.isFinite(value)) return '最近'
  const diff = Date.now() - value
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.max(1, Math.floor(diff / 60_000))} 分钟前`
  if (diff < 86_400_000) return `${Math.max(1, Math.floor(diff / 3_600_000))} 小时前`
  if (diff < 172_800_000) return '昨天'
  return new Date(input).toLocaleDateString('zh-CN')
}

function sessionDescription(meta: Record<string, string>) {
  return `${VIDEOHUB_META_PREFIX}${JSON.stringify(meta)}`
}

function parseSessionMeta(description?: string): Record<string, string> {
  const value = (description || '').trim()
  if (!value.startsWith(VIDEOHUB_META_PREFIX)) return {}
  try {
    const parsed = JSON.parse(value.slice(VIDEOHUB_META_PREFIX.length))
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function isVideohubSession(session: WeKnoraSession) {
  return (session.description || '').trim().startsWith(VIDEOHUB_META_PREFIX)
}

function unwrapData<T>(response: ApiEnvelope<T> | T): T {
  if (response && typeof response === 'object' && 'data' in response) {
    return (response as ApiEnvelope<T>).data as T
  }
  return response as T
}

function mapMessage(message: WeKnoraMessage, evidenceByKnowledgeID = new Map<string, EvidenceLookupItem>()): ChatMessage {
  const evidenceLinks = evidenceLinksFromReferences(message.knowledge_references || [], evidenceByKnowledgeID)
  const firstEvidence = evidenceLinks.find(item => item.videoId)
  const rawContent = message.content || ''
  return {
    id: message.id || messageId(),
    sender: message.role === 'user' ? 'user' : 'assistant',
    text: message.role === 'user' ? displayQuestionFromStoredContent(rawContent) : cleanAnswer(rawContent),
    timestamp: formatRelativeTime(message.created_at || message.updated_at),
    relatedVideoId: firstEvidence?.videoId,
    relatedVideoTitle: firstEvidence?.videoTitle,
    relatedTime: firstEvidence?.seconds,
    evidenceLinks,
  }
}

function evidenceLinksFromReferences(references: KnowledgeReference[], evidenceByKnowledgeID: Map<string, EvidenceLookupItem>): EvidenceLink[] {
  const result: EvidenceLink[] = []
  const seen = new Set<string>()
  for (const reference of references) {
    const knowledgeID = reference.knowledge_id || ''
    const evidence = evidenceByKnowledgeID.get(knowledgeID)
    const metadata = reference.metadata || {}
    const metadataStartMs = Number(metadata.start_ms)
    const hasMetadataStart = Number.isFinite(metadataStartMs) && metadataStartMs >= 0
    const seconds = hasMetadataStart
      ? Math.floor(metadataStartMs / 1000)
      : evidence?.seconds ?? 0
    const label = evidence?.video_title || reference.knowledge_title || metadata.source_filename || '知识来源'
    const timestamp = hasMetadataStart ? formatTime(seconds) : evidence?.timestamp || formatTime(seconds)
    const key = `${knowledgeID}:${seconds}:${label}`
    if (seen.has(key)) continue
    seen.add(key)
    result.push({
      label,
      timestamp,
      seconds,
      videoId: evidence?.video_id || metadata.video_id,
      videoTitle: evidence?.video_title || label,
    })
  }
  return result
}

function questionForScope(question: string, scope: ScopeResponse) {
  if (scope.scope !== 'video') return question
  return [
    `用户正在视频详情页围绕《${scope.video_title || '当前视频'}》提问。当前视频 ID：${scope.video_id || 'unknown'}。`,
    '请优先检索并引用当前视频的转写内容；如果当前视频信息不足，必须允许使用同一知识库中的其他视频或全局知识补充。',
    '引用当前视频之外的内容时，必须明确标注来源视频，不要让用户误以为全部来自当前视频。',
    `用户问题：${question}`,
  ].join('\n')
}

async function getScope(options?: SendOptions): Promise<ScopeResponse> {
  if (options?.currentVideo && !options.globalMode && options.currentVideo.id !== '__global__') {
    const res = await get<ApiEnvelope<ScopeResponse>>(`/api/custom/videos/${options.currentVideo.id}/chat-scope`)
    return unwrapData(res)
  }
  const res = await get<ApiEnvelope<ScopeResponse>>('/api/custom/chat/scope/global')
  return unwrapData(res)
}

async function createSession(question: string, scope: ScopeResponse): Promise<WeKnoraSession> {
  const titlePrefix = scope.scope === 'video' && scope.video_title ? `《${scope.video_title}》` : '全局视频问答'
  const title = `${titlePrefix}：${question}`.slice(0, 80)
  const res = await post<ApiEnvelope<WeKnoraSession>>('/api/v1/sessions', {
    title,
    description: sessionDescription(scope.session_meta || { scope: scope.scope }),
  })
  return unwrapData(res)
}

async function loadSessionMessages(sessionID: string): Promise<WeKnoraMessage[]> {
  const res = await get<ApiEnvelope<WeKnoraMessage[]>>(`/api/v1/messages/${sessionID}/load?limit=100`)
  return unwrapData(res) || []
}

async function lookupEvidence(knowledgeIDs: string[]) {
  const ids = [...new Set(knowledgeIDs.filter(Boolean))]
  if (!ids.length) return new Map<string, EvidenceLookupItem>()
  const query = encodeURIComponent(ids.join(','))
  const res = await get<ApiEnvelope<EvidenceLookupItem[]>>(`/api/custom/chat/evidence?knowledge_ids=${query}`)
  return new Map((unwrapData(res) || []).map(item => [item.knowledge_id, item]))
}

async function mapMessages(messages: WeKnoraMessage[]) {
  const knowledgeIDs = messages.flatMap(message => (message.knowledge_references || []).map(item => item.knowledge_id || '')).filter(Boolean)
  const evidence = await lookupEvidence(knowledgeIDs)
  return messages.filter(message => message.role === 'user' || message.role === 'assistant').map(message => mapMessage(message, evidence))
}

function cloneAgentEventStream(events: Record<string, unknown>[]) {
  return events.map(event => ({ ...event }))
}

function parseStreamPayload(raw: string): WeKnoraStreamPayload | null {
  if (!raw || raw === '[DONE]') return null
  try {
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : null
  } catch {
    return null
  }
}

function streamType(payload: WeKnoraStreamPayload) {
  return String(payload.response_type || payload.type || '').trim()
}

function appendNativeAgentEvent(
  events: Record<string, unknown>[],
  eventMap: Map<string, Record<string, unknown>>,
  pendingToolCalls: Map<string, Record<string, unknown>>,
  payload: WeKnoraStreamPayload,
  answer: string,
) {
  const type = streamType(payload)
  const data = payload.data || {}
  if (type === 'thinking' || type === 'reflection') {
    const eventID = String(data.event_id || payload.id || 'thinking')
    let event = eventMap.get(eventID)
    if (!event) {
      event = { type: 'thinking', event_id: eventID, content: '', done: false, startTime: Date.now(), thinking: true }
      events.push(event)
      eventMap.set(eventID, event)
    }
    if (payload.content && !payload.done) event.content = String(event.content || '') + payload.content
    if (payload.done) {
      event.done = true
      event.thinking = false
      event.duration_ms = data.duration_ms || Date.now() - Number(event.startTime || Date.now())
      event.completed_at = data.completed_at || Date.now()
    }
    return
  }

  if (type === 'tool_call') {
    const toolName = String(data.tool_name || data.name || '')
    if (toolName === 'final_answer') return
    const toolCallID = String(data.tool_call_id || (toolName ? `${toolName}-${events.length}` : payload.id || `tool-${events.length}`))
    let event = pendingToolCalls.get(toolCallID) || events.find(item => item.type === 'tool_call' && item.tool_call_id === toolCallID)
    if (!event) {
      event = { type: 'tool_call', tool_call_id: toolCallID, timestamp: Date.now(), pending: true }
      events.push(event)
    }
    event.tool_name = toolName || event.tool_name
    event.arguments = data.arguments || event.arguments
    event.pending = true
    event.tool_data = data
    pendingToolCalls.set(toolCallID, event)
    return
  }

  if (type === 'tool_result' || (type === 'error' && (data.tool_call_id || data.tool_name))) {
    const toolCallID = String(data.tool_call_id || '')
    const toolName = String(data.tool_name || data.name || '')
    let event = toolCallID ? pendingToolCalls.get(toolCallID) : undefined
    if (!event && toolName) {
      event = [...pendingToolCalls.values()].find(item => item.tool_name === toolName)
    }
    if (!event) {
      event = { type: 'tool_call', tool_call_id: toolCallID || `${toolName || 'tool'}-${events.length}`, tool_name: toolName, timestamp: Date.now() }
      events.push(event)
    }
    event.pending = false
    event.success = type !== 'error' && data.success !== false
    event.output = event.success ? data.output || payload.content : undefined
    event.error = event.success ? undefined : data.error || payload.content
    event.duration = data.duration_ms || data.duration
    event.duration_ms = data.duration_ms || data.duration
    event.display_type = data.display_type
    event.tool_data = data
    if (toolCallID) pendingToolCalls.delete(toolCallID)
    return
  }

  if (type === 'answer') {
    const eventID = String(data.event_id || '')
    const eventKey = eventID || 'answer'
    let event = eventMap.get(eventKey)
    if (!event) {
      event = { type: 'answer', event_id: eventID || undefined, content: '', done: false }
      events.push(event)
      eventMap.set(eventKey, event)
    }
    event.content = cleanAnswer(answer)
    if (payload.done) event.done = true
    if (data.is_fallback) event.is_fallback = true
    return
  }

  if (type === 'complete' || (!type && payload.done)) {
    let answerEvent = eventMap.get('answer')
    const cleanedAnswer = cleanAnswer(answer)
    if (!answerEvent && cleanedAnswer) {
      answerEvent = { type: 'answer', content: cleanedAnswer, done: false }
      events.push(answerEvent)
      eventMap.set('answer', answerEvent)
    }
    if (answerEvent) {
      answerEvent.content = cleanedAnswer || answerEvent.content || ''
      answerEvent.done = true
    }
    if (!events.some(event => event.type === 'agent_complete')) {
      events.push({
        type: 'agent_complete',
        total_duration_ms: data.total_duration_ms || 0,
        total_steps: data.total_steps || events.filter(event => event.type !== 'answer').length,
      })
    }
  }
}

function finalizeNativeAgentStream(
  events: Record<string, unknown>[],
  eventMap: Map<string, Record<string, unknown>>,
  answer: string,
) {
  const cleanedAnswer = cleanAnswer(answer)
  if (cleanedAnswer) {
    let answerEvent = eventMap.get('answer')
    if (!answerEvent) {
      answerEvent = { type: 'answer', content: cleanedAnswer, done: false }
      events.push(answerEvent)
      eventMap.set('answer', answerEvent)
    }
    answerEvent.content = cleanedAnswer
    answerEvent.done = true
  }
  if (!events.some(event => event.type === 'agent_complete')) {
    events.push({
      type: 'agent_complete',
      total_duration_ms: 0,
      total_steps: events.filter(event => event.type !== 'answer').length,
    })
  }
}

function findLastAssistantAnswerIndex(messages: ChatMessage[]) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message.sender === 'assistant' && message.text.trim()) return index
  }
  return -1
}

async function streamAnswer(sessionID: string, question: string, scope: ScopeResponse, options: StreamAnswerOptions = {}): Promise<StreamingAnswerResult> {
  const token = localStorage.getItem('weknora_token')
  if (!token) throw new Error('登录已失效，请重新登录后再提问')
  const apiBase = getApiBaseUrl()
  const selectedTenantId = localStorage.getItem('weknora_selected_tenant_id')
  let answer = ''
  let thinkingText = ''
  let activityText = ''
  let completed = false
  const agentEventStream: Record<string, unknown>[] = []
  const eventMap = new Map<string, Record<string, unknown>>()
  const pendingToolCalls = new Map<string, Record<string, unknown>>()
  const streamController = new AbortController()
  const emitChunk = () => options.onChunk?.({
    id: `stream-${sessionID}`,
    sender: 'assistant',
    text: cleanAnswer(answer),
    role: 'assistant',
    content: cleanAnswer(answer),
    isAgentMode: Boolean(scope.agent_id) || agentEventStream.length > 0,
    is_completed: completed,
    request_id: sessionID,
    agentEventStream: cloneAgentEventStream(agentEventStream),
    thinkingText: thinkingText.trim(),
    activityText,
    timestamp: nowLabel(),
  })
  const endpoint = scope.agent_id ? 'agent-chat' : 'knowledge-chat'
  await fetchEventSource(`${apiBase}/api/v1/${endpoint}/${sessionID}`, {
    method: 'POST',
    signal: streamController.signal,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
      ...(selectedTenantId ? { 'X-Tenant-ID': selectedTenantId } : {}),
    },
    body: JSON.stringify({
      query: questionForScope(question, scope),
      knowledge_base_ids: scope.knowledge_base_ids,
      // Video scope deliberately leaves knowledge_ids empty so the agent can
      // fall back to other global knowledge when the current video is not
      // sufficient.
      ...(scope.scope === 'global' || !scope.agent_id ? { knowledge_ids: scope.knowledge_ids } : {}),
      agent_enabled: Boolean(scope.agent_id),
      ...(scope.agent_id ? { agent_id: scope.agent_id } : {}),
      disable_title: true,
      channel: 'web',
    }),
    openWhenHidden: true,
    onopen: async response => {
      if (!response.ok) throw new Error(`问答请求失败：HTTP ${response.status}`)
    },
    onmessage: event => {
      const payload = parseStreamPayload(event.data)
      const chunk = parseWeKnoraStreamChunk(event.data)
      if (chunk.kind === 'answer') {
        answer += chunk.content || ''
        activityText = ''
      }
      if (chunk.kind === 'thinking') {
        thinkingText += chunk.content || ''
        activityText = '正在思考'
      }
      if (chunk.kind === 'activity') {
        activityText = chunk.content || ''
      }
      if (chunk.kind === 'complete' && !answer && chunk.content) {
        answer = chunk.content
        activityText = ''
      }
      if (payload) {
        appendNativeAgentEvent(agentEventStream, eventMap, pendingToolCalls, payload, answer)
        if (streamType(payload) === 'complete' || (!streamType(payload) && payload.done)) completed = true
      }
      if (chunk.kind === 'error') {
        throw new Error(chunk.content || '问答生成失败')
      }
      if (chunk.kind !== 'ignore' || payload) emitChunk()
      if (chunk.done) streamController.abort()
    },
    onerror: error => {
      throw error
    },
  })
  completed = true
  finalizeNativeAgentStream(agentEventStream, eventMap, answer)
  emitChunk()
  const streamMessage: StreamingChatMessage = {
    id: `stream-${sessionID}`,
    sender: 'assistant',
    text: cleanAnswer(answer),
    role: 'assistant',
    content: cleanAnswer(answer),
    isAgentMode: Boolean(scope.agent_id) || agentEventStream.length > 0,
    is_completed: true,
    request_id: sessionID,
    agentEventStream: cloneAgentEventStream(agentEventStream),
    thinkingText: thinkingText.trim(),
    activityText: '',
    timestamp: nowLabel(),
  }
  return { answer: cleanAnswer(answer), streamMessage }
}

function cleanAnswer(text: string) {
  return text
    .replace(/<think\b[^>]*>[\s\S]*?<\/think>/gi, '')
    .replace(/<think\b[^>]*>[\s\S]*$/gi, '')
    .replace(/<kb\b[^>]*\/?>/gi, '')
    .replace(/<web\b[^>]*\/?>/gi, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

async function hydrateLastAssistant(session: WeKnoraSession, fallbackText: string): Promise<ChatMessage> {
  const messages = await loadSessionMessages(session.id)
  const mapped = await mapMessages(messages)
  const assistant = [...mapped].reverse().find(message => message.sender === 'assistant' && message.text.trim())
  return assistant || { id: messageId(), sender: 'assistant', text: fallbackText, timestamp: nowLabel() }
}

function sessionFromScope(session: WeKnoraSession, scope: ScopeResponse, messages: ChatMessage[], fallbackTitle: string): ChatSession {
  return {
    id: session.id,
    title: session.title || fallbackTitle.slice(0, 24),
    type: scope.scope === 'video' ? 'video' : 'chat',
    time: formatRelativeTime(session.updated_at || session.created_at) || '刚刚',
    messages,
    scope: scope.scope,
    videoId: scope.video_id,
    videoTitle: scope.video_title,
    videoCoverUrl: scope.video_cover_url,
  }
}

export async function fetchSessions(): Promise<ChatSession[]> {
  const res = await get<ApiEnvelope<WeKnoraSession[]>>('/api/v1/sessions?page=1&page_size=20')
  return (unwrapData(res) || []).filter(isVideohubSession).map(session => {
    const meta = parseSessionMeta(session.description)
    const scope = meta.scope === 'video' ? 'video' : 'global'
    return {
      id: session.id,
      title: session.title || '未命名会话',
      type: scope === 'video' ? 'video' : 'chat',
      time: formatRelativeTime(session.updated_at || session.created_at),
      messages: [],
      scope,
      videoId: meta.video_id,
      videoTitle: meta.video_title,
      videoCoverUrl: meta.video_cover_url,
    }
  })
}

export async function loadChatSession(session: ChatSession): Promise<ChatSession> {
  const messages = await mapMessages(await loadSessionMessages(session.id))
  return { ...session, messages }
}

export async function sendChatMessage(question: string, options: SendOptions = {}): Promise<ChatMessage> {
  const scope = await getScope(options)
  const session = await createSession(question, scope)
  const { answer } = await streamAnswer(session.id, question, scope, { onChunk: options.onStreamMessage })
  const assistant = await hydrateLastAssistant(session, answer)
  options.onMessage?.(assistant)
  return assistant
}

export async function createChatTurn(question: string, options: TurnOptions = {}): Promise<ChatSession> {
  const scope = await getScope(options)
  const session = options.session?.id && !options.session.id.startsWith('pending-')
    ? {
      id: options.session.id,
      title: options.session.title,
      created_at: undefined,
      updated_at: undefined,
    }
    : await createSession(question, scope)
  const userMessage: ChatMessage = { id: messageId(), sender: 'user', text: question, timestamp: nowLabel() }
  options.onMessage?.(userMessage)
  const { answer, streamMessage } = await streamAnswer(session.id, question, scope, { onChunk: options.onStreamMessage })
  const messages = await mapMessages(await loadSessionMessages(session.id))
  const finalMessages = mergeLocalTurnWithStoredMessages(messages, userMessage, { ...streamMessage, id: messageId(), text: answer, timestamp: nowLabel() })
  return sessionFromScope(session, scope, finalMessages, question)
}

export async function sendAssistantQuery(question: string, currentVideo: VideoData, currentTime: number, globalMode = false): Promise<ChatMessage> {
  return sendChatMessage(question, { currentVideo, currentTime, globalMode })
}
