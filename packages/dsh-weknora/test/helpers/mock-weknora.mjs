/**
 * A stand-in WeKnora backend used by the unit tests and by the end-to-end run
 * inside dsh. It speaks the same routes, envelopes and SSE event types as the
 * real server (see internal/handler/session/qa.go and internal/types/chat.go),
 * so a test failure here means the plugin, not the fixture, drifted.
 */

import { createServer } from 'node:http'

const DOCUMENTS = [
  {
    knowledge_id: 'doc-retrieval-pipeline',
    knowledge_title: 'WeKnora 检索流程.md',
    chunks: [
      'WeKnora 的混合检索先做向量召回，再做关键词召回，最后交给 rerank 模型统一排序。',
      '默认的 vector_threshold 是 0.5，keyword_threshold 是 0.3，可以在知识库配置里按库调整。',
      '父子分块开启后，命中子块会回溯父块，把更完整的上下文交给生成模型。',
    ],
  },
  {
    knowledge_id: 'doc-deployment',
    knowledge_title: 'WeKnora 部署手册.md',
    chunks: [
      'WeKnora 支持 docker compose 单机部署，也提供 Helm chart 用于 Kubernetes。',
      '向量库可以选择 pgvector、Milvus、Qdrant、Weaviate、OpenSearch 等后端。',
    ],
  },
]

const KNOWLEDGE_BASES = [
  { id: 'kb-product', name: 'Product docs', description: 'WeKnora 产品与检索文档' },
  { id: 'kb-ops', name: 'Ops runbooks', description: '部署与运维手册' },
]

/**
 * Score a passage by character-bigram overlap. The real backend scores with
 * embeddings plus keyword match; bigrams are enough to make the fixture rank
 * plausibly for both Chinese and English queries.
 */
function scoreOf(query, content) {
  const normalized = query.replace(/[\s,，。？?、！!：:；;“”"']+/g, '')
  const shingles = new Set()
  for (let index = 0; index + 2 <= normalized.length; index += 1) {
    shingles.add(normalized.slice(index, index + 2))
  }
  if (shingles.size === 0) return 0
  const hits = [...shingles].filter(shingle => content.includes(shingle)).length
  return Math.min(0.99, 0.4 + (hits / shingles.size) * 0.5)
}

function searchResults(query, knowledgeBaseIds, knowledgeIds) {
  const documents = DOCUMENTS.filter(document =>
    (knowledgeIds.length === 0 || knowledgeIds.includes(document.knowledge_id))
    && (knowledgeBaseIds.length === 0
      || knowledgeBaseIds.includes(document.knowledge_id === 'doc-deployment' ? 'kb-ops' : 'kb-product')))
  return documents
    .flatMap(document => document.chunks.map((content, index) => ({
      id: `${document.knowledge_id}-chunk-${index}`,
      content,
      knowledge_id: document.knowledge_id,
      knowledge_title: document.knowledge_title,
      chunk_index: index,
      score: scoreOf(query, content),
      match_type: 2,
      start_at: 0,
      end_at: content.length,
      metadata: {},
    })))
    .filter(result => result.score > 0.4)
    .sort((left, right) => right.score - left.score)
}

/** Assemble the answer the fake RAG/agent pipeline streams back. */
function answerFor(query, results) {
  const cited = results.slice(0, 2).map(result => result.content).join(' ')
  return `根据知识库内容：${cited}（问题：${query}）`
}

/**
 * Start the mock backend.
 * @param options.apiKey - when set, requests must carry it as `X-API-Key`.
 * @param options.streamError - make the chat routes stream an `error` event.
 * @returns the base URL, the recorded requests, and a close function.
 */
export async function startMockWeknora(options = {}) {
  const requests = []
  const server = createServer((request, response) => {
    const url = new URL(request.url, 'http://mock.invalid')
    const chunks = []
    request.on('data', chunk => chunks.push(chunk))
    request.on('end', () => {
      const rawBody = Buffer.concat(chunks).toString('utf8')
      let body = {}
      if (rawBody !== '') {
        try {
          body = JSON.parse(rawBody)
        } catch {
          body = { _unparsed: rawBody }
        }
      }
      requests.push({
        method: request.method,
        path: url.pathname,
        query: Object.fromEntries(url.searchParams),
        headers: request.headers,
        body,
      })

      const json = (status, payload) => {
        response.writeHead(status, { 'Content-Type': 'application/json' })
        response.end(JSON.stringify(payload))
      }

      if (options.apiKey !== undefined && request.headers['x-api-key'] !== options.apiKey) {
        json(401, { success: false, error: { message: 'invalid api key' } })
        return
      }

      if (request.method === 'GET' && url.pathname === '/api/v1/knowledge-bases') {
        json(200, { success: true, data: KNOWLEDGE_BASES })
        return
      }

      if (request.method === 'POST' && url.pathname === '/api/v1/knowledge-search') {
        if (typeof body.query !== 'string' || body.query === '') {
          json(400, { success: false, error: { message: 'Query content cannot be empty' } })
          return
        }
        // Mirror the real handler, which refuses an unscoped retrieval
        // (internal/handler/session/qa.go). Answering one here would let the
        // plugin ship a call every deployment rejects.
        if ((body.knowledge_base_ids ?? []).length === 0
          && (body.knowledge_ids ?? []).length === 0
          && (body.tag_ids ?? []).length === 0) {
          json(400, {
            success: false,
            error: {
              message: 'At least one knowledge_base_id, knowledge_base_ids, knowledge_ids, or scoped tag must be provided',
            },
          })
          return
        }
        json(200, {
          success: true,
          data: searchResults(body.query, body.knowledge_base_ids ?? [], body.knowledge_ids ?? []),
        })
        return
      }

      if (request.method === 'GET' && url.pathname.startsWith('/api/v1/chunks/')) {
        const knowledgeId = decodeURIComponent(url.pathname.slice('/api/v1/chunks/'.length))
        const document = DOCUMENTS.find(entry => entry.knowledge_id === knowledgeId)
        if (document === undefined) {
          json(404, { success: false, error: { message: 'knowledge not found' } })
          return
        }
        const page = Number(url.searchParams.get('page') ?? '1')
        const pageSize = Number(url.searchParams.get('page_size') ?? '10')
        const start = (page - 1) * pageSize
        json(200, {
          success: true,
          data: document.chunks.slice(start, start + pageSize).map((content, index) => ({
            id: `${knowledgeId}-chunk-${start + index}`,
            knowledge_id: knowledgeId,
            knowledge_base_id: 'kb-product',
            content,
            chunk_index: start + index,
            is_enabled: true,
          })),
          total: document.chunks.length,
          page,
          page_size: pageSize,
        })
        return
      }

      if (request.method === 'POST' && url.pathname === '/api/v1/sessions') {
        json(201, { success: true, data: { id: 'session-mock-1', title: body.title ?? '' } })
        return
      }

      const chatMatch = /^\/api\/v1\/(knowledge-chat|agent-chat)\/(.+)$/.exec(url.pathname)
      if (request.method === 'POST' && chatMatch !== null) {
        const [, route, sessionId] = chatMatch
        response.writeHead(200, {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          'Connection': 'keep-alive',
        })
        const send = payload => response.write(`event:message\ndata:${JSON.stringify(payload)}\n\n`)
        if (options.streamError === true) {
          send({ id: 'e1', response_type: 'error', content: 'model provider unavailable', done: true })
          response.end()
          return
        }
        const results = searchResults(body.query ?? '', body.knowledge_base_ids ?? [], [])
        if (route === 'agent-chat') {
          send({
            id: 't1',
            response_type: 'tool_call',
            content: '',
            done: false,
            session_id: sessionId,
            tool_calls: [{ id: 'call-1', function: { name: 'knowledge_search', arguments: '{}' } }],
          })
        }
        send({ id: 'r1', response_type: 'references', content: '', done: false, knowledge_references: results })
        for (const piece of answerFor(body.query ?? '', results).match(/.{1,24}/gs) ?? []) {
          send({ id: 'a1', response_type: 'answer', content: piece, done: false, session_id: sessionId })
        }
        send({ id: 'c1', response_type: 'complete', content: '', done: true, session_id: sessionId })
        response.end()
        return
      }

      json(404, { success: false, error: { message: `no route for ${request.method} ${url.pathname}` } })
    })
  })

  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  return {
    url: `http://127.0.0.1:${port}/api/v1`,
    requests,
    async close() {
      await new Promise(resolve => server.close(resolve))
    },
  }
}

export { DOCUMENTS, KNOWLEDGE_BASES }
