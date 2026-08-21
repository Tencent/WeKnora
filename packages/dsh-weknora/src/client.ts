/** Minimal WeKnora REST client: JSON endpoints plus the SSE chat streams. */

import type { ResolvedConfig } from './config.ts'

/** One retrieval hit, as returned by WeKnora's `SearchResult`. */
export interface SearchResult {
  id?: string
  content?: string
  knowledge_id?: string
  chunk_index?: number
  knowledge_title?: string
  knowledge_filename?: string
  score?: number
  match_type?: number | string
  [key: string]: unknown
}

/** A knowledge base as listed by `GET /knowledge-bases`. */
export interface KnowledgeBaseSummary {
  id?: string
  name?: string
  description?: string
  [key: string]: unknown
}

/** One stored chunk as returned by `GET /chunks/:knowledge_id`. */
export interface ChunkRecord {
  id?: string
  content?: string
  chunk_index?: number
  knowledge_id?: string
  [key: string]: unknown
}

/** A page of chunks plus its pagination envelope. */
export interface ChunkPage {
  chunks: ChunkRecord[]
  total: number
  page: number
  pageSize: number
}

/** The assembled outcome of one streamed answer. */
export interface StreamedAnswer {
  answer: string
  references: SearchResult[]
  sessionId: string
  toolCalls: string[]
}

/** Raised for any non-2xx response or transport failure, with the key redacted. */
export class WeknoraApiError extends Error {
  readonly status: number | undefined

  constructor(message: string, status?: number) {
    super(message)
    this.name = 'WeknoraApiError'
    this.status = status
  }
}

interface Envelope<T> {
  success?: boolean
  data?: T
  error?: unknown
  message?: string
  total?: number
  page?: number
  page_size?: number
}

/** Trim a response body for an error message without leaking a whole page. */
function snippet(body: string): string {
  const collapsed = body.replace(/\s+/g, ' ').trim()
  return collapsed.length > 300 ? `${collapsed.slice(0, 300)}…` : collapsed
}

/** Extract a human-readable reason from WeKnora's error envelope. */
function reasonOf(body: string): string {
  try {
    const parsed = JSON.parse(body) as { error?: { message?: string } | string, message?: string }
    if (typeof parsed.error === 'string') return parsed.error
    if (parsed.error?.message !== undefined) return parsed.error.message
    if (parsed.message !== undefined) return parsed.message
  } catch {
    // Fall through to the raw snippet: a proxy may answer with HTML.
  }
  return snippet(body)
}

/**
 * Combine the caller's cancellation with this call's deadline. The tool body
 * must abort when the harness aborts, and a hung backend must not hold a turn
 * open forever.
 */
function deadline(signal: AbortSignal, timeoutMs: number): AbortSignal {
  return AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)])
}

export class WeknoraClient {
  constructor(private readonly config: ResolvedConfig) {}

  private headers(): Record<string, string> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
    }
    if (this.config.apiKey !== undefined) headers['X-API-Key'] = this.config.apiKey
    if (this.config.tenantId !== undefined) headers['X-Tenant-ID'] = this.config.tenantId
    return headers
  }

  private url(path: string, query?: Record<string, string | number | undefined>): string {
    const url = new URL(`${this.config.baseUrl}${path}`)
    if (this.config.resourceUrls === 'public') url.searchParams.set('resource_urls', 'public')
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) url.searchParams.set(key, String(value))
    }
    return url.toString()
  }

  private async fetchJson<T>(
    path: string,
    init: { method: 'GET' | 'POST', body?: unknown, query?: Record<string, string | number | undefined> },
    signal: AbortSignal,
  ): Promise<Envelope<T>> {
    const url = this.url(path, init.query)
    let response: Response
    try {
      response = await fetch(url, {
        method: init.method,
        headers: this.headers(),
        ...init.body === undefined ? {} : { body: JSON.stringify(init.body) },
        signal: deadline(signal, this.config.requestTimeoutMs),
      })
    } catch (cause) {
      throw new WeknoraApiError(`${init.method} ${path} failed: ${describeTransportFailure(cause, signal)}`)
    }
    const text = await response.text()
    if (!response.ok) {
      throw new WeknoraApiError(`${init.method} ${path} failed with HTTP ${response.status}: ${reasonOf(text)}`, response.status)
    }
    try {
      return JSON.parse(text) as Envelope<T>
    } catch {
      throw new WeknoraApiError(`${init.method} ${path} returned a non-JSON body: ${snippet(text)}`, response.status)
    }
  }

  /** List the knowledge bases this credential can retrieve from. */
  async listKnowledgeBases(signal: AbortSignal): Promise<KnowledgeBaseSummary[]> {
    const envelope = await this.fetchJson<KnowledgeBaseSummary[]>('/knowledge-bases', { method: 'GET' }, signal)
    return Array.isArray(envelope.data) ? envelope.data : []
  }

  /** Hybrid (vector + keyword) retrieval across one or more knowledge bases. */
  async search(
    input: { query: string, knowledgeBaseIds: string[], knowledgeIds: string[] },
    signal: AbortSignal,
  ): Promise<SearchResult[]> {
    const envelope = await this.fetchJson<SearchResult[]>('/knowledge-search', {
      method: 'POST',
      body: {
        query: input.query,
        ...input.knowledgeBaseIds.length > 0 ? { knowledge_base_ids: input.knowledgeBaseIds } : {},
        ...input.knowledgeIds.length > 0 ? { knowledge_ids: input.knowledgeIds } : {},
      },
    }, signal)
    return Array.isArray(envelope.data) ? envelope.data : []
  }

  /** One page of a document's stored chunks, in storage order. */
  async listChunks(
    input: { knowledgeId: string, page: number, pageSize: number },
    signal: AbortSignal,
  ): Promise<ChunkPage> {
    const envelope = await this.fetchJson<ChunkRecord[]>(`/chunks/${encodeURIComponent(input.knowledgeId)}`, {
      method: 'GET',
      query: { page: input.page, page_size: input.pageSize },
    }, signal)
    const chunks = Array.isArray(envelope.data) ? envelope.data : []
    return {
      chunks,
      total: typeof envelope.total === 'number' ? envelope.total : chunks.length,
      page: typeof envelope.page === 'number' ? envelope.page : input.page,
      pageSize: typeof envelope.page_size === 'number' ? envelope.page_size : input.pageSize,
    }
  }

  /** Create a conversation container to hold one or more asked questions. */
  async createSession(title: string, signal: AbortSignal): Promise<string> {
    const envelope = await this.fetchJson<{ id?: string }>('/sessions', {
      method: 'POST',
      body: { title },
    }, signal)
    const id = envelope.data?.id
    if (typeof id !== 'string' || id === '') {
      throw new WeknoraApiError('POST /sessions returned no session id')
    }
    return id
  }

  /**
   * Ask a question and assemble the streamed answer. `agentId` selects the
   * ReAct pipeline (`/agent-chat`); without it the RAG pipeline answers
   * (`/knowledge-chat`).
   */
  async ask(
    input: {
      sessionId: string
      query: string
      knowledgeBaseIds: string[]
      agentId: string | undefined
      webSearch: boolean
    },
    signal: AbortSignal,
  ): Promise<StreamedAnswer> {
    const route = input.agentId === undefined ? 'knowledge-chat' : 'agent-chat'
    const path = `/${route}/${encodeURIComponent(input.sessionId)}`
    const body = {
      query: input.query,
      channel: 'api',
      ...input.knowledgeBaseIds.length > 0 ? { knowledge_base_ids: input.knowledgeBaseIds } : {},
      ...input.agentId === undefined ? {} : { agent_id: input.agentId, agent_enabled: true },
      ...input.webSearch ? { web_search_enabled: true } : {},
    }
    const combined = deadline(signal, this.config.chatTimeoutMs)
    let response: Response
    try {
      response = await fetch(this.url(path), {
        method: 'POST',
        headers: { ...this.headers(), Accept: 'text/event-stream' },
        body: JSON.stringify(body),
        signal: combined,
      })
    } catch (cause) {
      throw new WeknoraApiError(`POST ${path} failed: ${describeTransportFailure(cause, signal)}`)
    }
    if (!response.ok) {
      const text = await response.text()
      throw new WeknoraApiError(`POST ${path} failed with HTTP ${response.status}: ${reasonOf(text)}`, response.status)
    }
    if (response.body === null) {
      throw new WeknoraApiError(`POST ${path} returned no response body`)
    }
    return await assembleStream(response.body, input.sessionId, path, signal)
  }
}

/** Turn an aborted or refused fetch into a message that names the real cause. */
function describeTransportFailure(cause: unknown, callerSignal: AbortSignal): string {
  if (cause instanceof Error && cause.name === 'AbortError') {
    return callerSignal.aborted ? 'the call was cancelled' : 'the request timed out'
  }
  if (cause instanceof Error && cause.name === 'TimeoutError') return 'the request timed out'
  return cause instanceof Error ? cause.message : String(cause)
}

/** One decoded `data:` payload of the WeKnora stream. */
interface StreamEvent {
  response_type?: string
  content?: string
  knowledge_references?: SearchResult[]
  session_id?: string
  tool_calls?: { function?: { name?: string }, name?: string }[]
}

/**
 * Consume WeKnora's `text/event-stream` and assemble the parts a tool result
 * needs: the answer text, its citations, and the tool names the agent used.
 * Server-side `error` events become a thrown failure so the model is never
 * handed a silently empty answer.
 */
async function assembleStream(
  body: ReadableStream<Uint8Array>,
  sessionId: string,
  path: string,
  callerSignal: AbortSignal,
): Promise<StreamedAnswer> {
  const decoder = new TextDecoder()
  const reader = body.getReader()
  const answer: string[] = []
  const toolCalls: string[] = []
  let references: SearchResult[] = []
  let resolvedSessionId = sessionId
  let buffer = ''
  let completed = false

  const consumeLine = (line: string): void => {
    if (!line.startsWith('data:')) return
    const payload = line.slice(5).trim()
    if (payload === '' || payload === '[DONE]') return
    let event: StreamEvent
    try {
      event = JSON.parse(payload) as StreamEvent
    } catch {
      return
    }
    if (typeof event.session_id === 'string' && event.session_id !== '') resolvedSessionId = event.session_id
    switch (event.response_type) {
      case 'answer':
        if (typeof event.content === 'string') answer.push(event.content)
        break
      case 'references':
        if (Array.isArray(event.knowledge_references)) references = event.knowledge_references
        break
      case 'tool_call':
        for (const call of event.tool_calls ?? []) {
          const name = call.function?.name ?? call.name
          if (typeof name === 'string' && name !== '' && !toolCalls.includes(name)) toolCalls.push(name)
        }
        break
      case 'error':
        throw new WeknoraApiError(`POST ${path} streamed an error: ${event.content ?? 'unknown error'}`)
      case 'complete':
        completed = true
        break
      default:
        break
    }
  }

  try {
    while (!completed) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let newline = buffer.indexOf('\n')
      while (newline >= 0) {
        consumeLine(buffer.slice(0, newline).replace(/\r$/, ''))
        buffer = buffer.slice(newline + 1)
        if (completed) break
        newline = buffer.indexOf('\n')
      }
    }
    if (!completed && buffer !== '') consumeLine(buffer.replace(/\r$/, ''))
  } catch (cause) {
    if (cause instanceof WeknoraApiError) throw cause
    throw new WeknoraApiError(`POST ${path} stream failed: ${describeTransportFailure(cause, callerSignal)}`)
  } finally {
    await reader.cancel().catch(() => undefined)
  }

  return { answer: answer.join(''), references, sessionId: resolvedSessionId, toolCalls }
}
