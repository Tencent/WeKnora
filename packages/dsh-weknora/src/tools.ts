/** The model-facing tools this plugin contributes to `ctx.tools`. */

import type { ChunkRecord, DocumentRecord, SearchResult, WeknoraClient } from './client.ts'
import type { ResolvedConfig } from './config.ts'
import type { JsonSchemaNode, TextContentBlock, ToolDefinition } from './harness.ts'
import { clip, describeScope, formatScore } from './render.ts'

/** A retrieval hit projected onto the fields the model and follow-up calls need. */
interface SearchHit {
  rank: number
  chunk_id: string
  knowledge_id: string
  document: string
  chunk_index: number
  score: number
  content: string
  truncated: boolean
}

/** A document whose name matched, which passage retrieval alone would miss. */
interface DocumentMatch {
  knowledge_id: string
  title: string
  knowledge_base_id: string
  knowledge_base: string
}

interface SearchValue {
  query: string
  knowledge_base_ids: string[]
  count: number
  results: SearchHit[]
  documents: DocumentMatch[]
}

interface KnowledgeBasesValue {
  count: number
  knowledge_bases: { id: string, name: string, description: string }[]
}

interface DocumentValue {
  knowledge_id: string
  title: string
  summary: string
  page: number
  page_size: number
  total_chunks: number
  returned_chunks: number
  has_more: boolean
  truncated: boolean
  content: string
}

interface AskValue {
  answer: string
  session_id: string
  pipeline: 'rag' | 'agent'
  tool_calls: string[]
  references: { knowledge_id: string, document: string, chunk_index: number, content: string }[]
}

const text = (value: string): TextContentBlock[] => [{ type: 'text', text: value }]

const STRING_ARRAY: JsonSchemaNode = { type: 'array', items: { type: 'string' } }

/** Read an argument the harness passes as `unknown` without trusting its shape. */
function argRecord(args: unknown): Record<string, unknown> {
  return typeof args === 'object' && args !== null && !Array.isArray(args) ? args as Record<string, unknown> : {}
}

function requiredString(args: unknown, field: string, toolName: string): string {
  const value = argRecord(args)[field]
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`${toolName}: "${field}" is required and must be a non-empty string`)
  }
  return value.trim()
}

function optionalStringArg(args: unknown, field: string): string | undefined {
  const value = argRecord(args)[field]
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined
}

function stringArrayArg(args: unknown, field: string): string[] {
  const value = argRecord(args)[field]
  if (!Array.isArray(value)) return []
  return value.filter((entry): entry is string => typeof entry === 'string' && entry.trim() !== '').map(entry => entry.trim())
}

function boundedIntArg(args: unknown, field: string, fallback: number, max: number): number {
  const value = argRecord(args)[field]
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return fallback
  return Math.min(Math.floor(value), max)
}

/** Prefer a human title, then the source filename, then the opaque id. */
function documentLabel(result: SearchResult): string {
  const title = typeof result.knowledge_title === 'string' ? result.knowledge_title.trim() : ''
  if (title !== '') return title
  const filename = typeof result.knowledge_filename === 'string' ? result.knowledge_filename.trim() : ''
  if (filename !== '') return filename
  return typeof result.knowledge_id === 'string' && result.knowledge_id !== '' ? result.knowledge_id : '(untitled)'
}

function projectHit(result: SearchResult, rank: number, maxChunkChars: number): SearchHit {
  const clipped = clip(typeof result.content === 'string' ? result.content : '', maxChunkChars)
  return {
    rank,
    chunk_id: typeof result.id === 'string' ? result.id : '',
    knowledge_id: typeof result.knowledge_id === 'string' ? result.knowledge_id : '',
    document: documentLabel(result),
    chunk_index: typeof result.chunk_index === 'number' ? result.chunk_index : -1,
    // 0 rather than NaN: the canonical value must stay lossless JSON.
    score: typeof result.score === 'number' && Number.isFinite(result.score) ? result.score : 0,
    content: clipped.text,
    truncated: clipped.truncated,
  }
}

/**
 * Keep the by-name matches worth showing: inside the scope the caller asked
 * for, and not already represented by a passage hit. WeKnora's by-name search
 * spans the whole tenant, so the scope filter is what stops a narrowed call
 * from reporting documents outside it.
 */
function projectNamedDocuments(
  named: DocumentRecord[],
  passages: SearchHit[],
  knowledgeBaseIds: string[],
  knowledgeIds: string[],
): DocumentMatch[] {
  const already = new Set(passages.map(hit => hit.knowledge_id))
  const scope = new Set(knowledgeBaseIds)
  const wanted = new Set(knowledgeIds)
  const out: DocumentMatch[] = []
  for (const document of named) {
    const id = typeof document.id === 'string' ? document.id : ''
    const kbId = typeof document.knowledge_base_id === 'string' ? document.knowledge_base_id : ''
    if (id === '' || already.has(id)) continue
    if (wanted.size > 0 ? !wanted.has(id) : !scope.has(kbId)) continue
    const title = typeof document.title === 'string' && document.title.trim() !== ''
      ? document.title.trim()
      : typeof document.file_name === 'string' ? document.file_name : id
    out.push({
      knowledge_id: id,
      title,
      knowledge_base_id: kbId,
      knowledge_base: typeof document.knowledge_base_name === 'string' ? document.knowledge_base_name : kbId,
    })
    already.add(id)
  }
  return out
}

/** Assemble the four tool definitions for one configured deployment. */
export function createTools(client: WeknoraClient, config: ResolvedConfig): ToolDefinition[] {
  const name = (suffix: string): string => `${config.toolPrefix}_${suffix}`
  const scopeNote = config.knowledgeBaseIds.length > 0
    ? ` Searches knowledge base(s) ${config.knowledgeBaseIds.join(', ')} unless you name others.`
    : ' Searches every knowledge base this credential can see unless you narrow it with knowledge_base_ids.'

  // WeKnora refuses a retrieval that names no knowledge base, document or tag,
  // so an unconfigured deployment resolves the full visible set once and reuses
  // it. Making the model pick a scope first is worse: knowledge bases are often
  // named too poorly to choose between, and fanning out across them is cheap
  // while they share a vector store.
  let everyId: Promise<string[]> | undefined
  const allKnowledgeBaseIds = async (signal: AbortSignal): Promise<string[]> => {
    everyId ??= client.listKnowledgeBases(signal).then(
      bases => bases.map(kb => typeof kb.id === 'string' ? kb.id : '').filter(id => id !== ''),
      (error: unknown) => {
        everyId = undefined
        throw error
      },
    )
    return await everyId
  }
  const resolveScope = async (requested: string[], knowledgeIds: string[], signal: AbortSignal): Promise<string[]> => {
    if (requested.length > 0) return requested
    if (config.knowledgeBaseIds.length > 0) return config.knowledgeBaseIds
    if (knowledgeIds.length > 0) return []
    return await allKnowledgeBaseIds(signal)
  }

  const definitions: ToolDefinition[] = []

  if (config.tools.listKnowledgeBases) {
    definitions.push({
      name: name('list_knowledge_bases'),
      description: 'List the WeKnora knowledge bases this deployment can retrieve from, with their ids. '
        + `${name('search')} already spans them all, so reach for this only to report what is available or to `
        + 'narrow a later search to one of them.',
      parameters: { type: 'object', properties: {}, additionalProperties: false },
      timeoutMs: config.requestTimeoutMs,
      isConcurrencySafe: () => true,
      output: {
        schema: {
          type: 'object',
          properties: {
            count: { type: 'integer' },
            knowledge_bases: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  id: { type: 'string' },
                  name: { type: 'string' },
                  description: { type: 'string' },
                },
                required: ['id', 'name', 'description'],
                additionalProperties: false,
              },
            },
          },
          required: ['count', 'knowledge_bases'],
          additionalProperties: false,
        },
        render: (_args, value) => {
          const result = value as KnowledgeBasesValue
          if (result.count === 0) return text('No knowledge base is available to this WeKnora credential.')
          const lines = result.knowledge_bases.map(kb => {
            const description = kb.description === '' ? '' : ` — ${kb.description}`
            return `- ${kb.name} (id: ${kb.id})${description}`
          })
          return text(`${result.count} knowledge base(s):\n${lines.join('\n')}`)
        },
      },
      async execute(_args, exec): Promise<KnowledgeBasesValue> {
        const bases = await client.listKnowledgeBases(exec.signal)
        return {
          count: bases.length,
          knowledge_bases: bases.map(kb => ({
            id: typeof kb.id === 'string' ? kb.id : '',
            name: typeof kb.name === 'string' ? kb.name : '',
            description: typeof kb.description === 'string' ? kb.description : '',
          })),
        }
      },
    })
  }

  if (config.tools.search) {
    definitions.push({
      name: name('search'),
      description: 'Search WeKnora knowledge bases and return the matching passages verbatim (hybrid vector + keyword '
        + 'retrieval, no model summarization). Use this to ground an answer in the organization\'s own documents, and '
        + 'also to locate a document by name — a query that reads like a title additionally returns the documents it '
        + 'names.' + scopeNote
        + ` Every hit carries a knowledge_id you can pass to ${name('read_document')} for the full document.`,
      parameters: {
        type: 'object',
        properties: {
          query: { type: 'string', description: 'Natural-language query; a full question retrieves better than keywords.' },
          knowledge_base_ids: { ...STRING_ARRAY, description: 'Restrict the search to these knowledge base ids.' },
          knowledge_ids: { ...STRING_ARRAY, description: 'Restrict the search to these document ids.' },
          max_results: { type: 'integer', description: `Maximum passages to return (default ${config.maxResults}).` },
        },
        required: ['query'],
        additionalProperties: false,
      },
      timeoutMs: config.requestTimeoutMs,
      isConcurrencySafe: () => true,
      output: {
        schema: {
          type: 'object',
          properties: {
            query: { type: 'string' },
            knowledge_base_ids: STRING_ARRAY,
            count: { type: 'integer' },
            results: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  rank: { type: 'integer' },
                  chunk_id: { type: 'string' },
                  knowledge_id: { type: 'string' },
                  document: { type: 'string' },
                  chunk_index: { type: 'integer' },
                  score: { type: 'number' },
                  content: { type: 'string' },
                  truncated: { type: 'boolean' },
                },
                required: ['rank', 'chunk_id', 'knowledge_id', 'document', 'chunk_index', 'score', 'content', 'truncated'],
                additionalProperties: false,
              },
            },
            documents: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  knowledge_id: { type: 'string' },
                  title: { type: 'string' },
                  knowledge_base_id: { type: 'string' },
                  knowledge_base: { type: 'string' },
                },
                required: ['knowledge_id', 'title', 'knowledge_base_id', 'knowledge_base'],
                additionalProperties: false,
              },
            },
          },
          required: ['query', 'knowledge_base_ids', 'count', 'results', 'documents'],
          additionalProperties: false,
        },
        render: (_args, value) => {
          const result = value as SearchValue
          const named = result.documents.length === 0
            ? ''
            : `\n\nDocuments named "${result.query}" (read them with ${name('read_document')}):\n`
              + result.documents.map(document =>
                `- ${document.title} · knowledge_id: ${document.knowledge_id} · in ${document.knowledge_base}`).join('\n')
          if (result.count === 0) {
            if (named === '') {
              return text(`Nothing in WeKnora matched "${result.query}" `
                + `(searched: ${describeScope(result.knowledge_base_ids)}). `
                + 'Try a differently worded query, or widen the knowledge base scope.')
            }
            return text(`No passage matched "${result.query}", but its name matches document(s).${named}`)
          }
          const blocks = result.results.map(hit =>
            `[${hit.rank}] ${hit.document} · score ${formatScore(hit.score)} · chunk ${hit.chunk_index} `
            + `· knowledge_id: ${hit.knowledge_id}\n${hit.content}${hit.truncated ? '\n(passage truncated)' : ''}`)
          return text(`${result.count} passage(s) for "${result.query}" `
            + `(searched: ${describeScope(result.knowledge_base_ids)}):\n\n${blocks.join('\n\n')}${named}`)
        },
      },
      async execute(args, exec): Promise<SearchValue> {
        const toolName = name('search')
        const query = requiredString(args, 'query', toolName)
        const knowledgeIds = stringArrayArg(args, 'knowledge_ids')
        const knowledgeBaseIds = await resolveScope(
          stringArrayArg(args, 'knowledge_base_ids'),
          knowledgeIds,
          exec.signal,
        )
        // Fail here rather than let WeKnora answer 400: the model can act on a
        // message naming the argument to supply, not on a transport error.
        if (knowledgeBaseIds.length === 0 && knowledgeIds.length === 0) {
          throw new Error(`${toolName}: this WeKnora credential can see no knowledge base, so there is `
            + 'nothing to search. Check the deployment\'s API key scope.')
        }
        const limit = boundedIntArg(args, 'max_results', config.maxResults, config.maxResults)
        const [hits, named] = await Promise.all([
          client.search({ query, knowledgeBaseIds, knowledgeIds }, exec.signal),
          // By-name matching is an enrichment, and older WeKnora builds may not
          // route it. Losing it must not cost the model its passages.
          client.findDocuments({ keyword: query, limit }, exec.signal).catch(() => []),
        ])
        const results = hits.slice(0, limit).map((hit, index) => projectHit(hit, index + 1, config.maxChunkChars))
        return {
          query,
          knowledge_base_ids: knowledgeBaseIds,
          count: results.length,
          results,
          documents: projectNamedDocuments(named, results, knowledgeBaseIds, knowledgeIds),
        }
      },
    })
  }

  if (config.tools.readDocument) {
    definitions.push({
      name: name('read_document'),
      description: 'Read a WeKnora document\'s stored passages in order, reassembled into text. '
        + `Use it after ${name('search')} when one passage is not enough context; page through long documents.`,
      parameters: {
        type: 'object',
        properties: {
          knowledge_id: { type: 'string', description: 'Document id, as returned in a search hit.' },
          page: { type: 'integer', description: 'Page of passages, starting at 1.' },
          page_size: { type: 'integer', description: 'Passages per page (max 100).' },
        },
        required: ['knowledge_id'],
        additionalProperties: false,
      },
      timeoutMs: config.requestTimeoutMs,
      isConcurrencySafe: () => true,
      output: {
        schema: {
          type: 'object',
          properties: {
            knowledge_id: { type: 'string' },
            title: { type: 'string' },
            summary: { type: 'string' },
            page: { type: 'integer' },
            page_size: { type: 'integer' },
            total_chunks: { type: 'integer' },
            returned_chunks: { type: 'integer' },
            has_more: { type: 'boolean' },
            truncated: { type: 'boolean' },
            content: { type: 'string' },
          },
          required: [
            'knowledge_id', 'title', 'summary', 'page', 'page_size',
            'total_chunks', 'returned_chunks', 'has_more', 'truncated', 'content',
          ],
          additionalProperties: false,
        },
        render: (_args, value) => {
          const result = value as DocumentValue
          const label = result.title === '' ? result.knowledge_id : `${result.title} (${result.knowledge_id})`
          if (result.returned_chunks === 0) {
            return text(`Document ${label} has no passage on page ${result.page} `
              + `(${result.total_chunks} passage(s) in total).`)
          }
          // The summary lets the model judge a long document from page 1
          // instead of paging blindly to find out what it is holding.
          const summary = result.summary === '' ? '' : `\n\nSummary: ${result.summary}`
          const more = result.has_more ? `\n\n(more passages available: request page ${result.page + 1})` : ''
          const cut = result.truncated ? '\n(content truncated)' : ''
          return text(`Document ${label}, passages ${result.returned_chunks} of ${result.total_chunks} `
            + `(page ${result.page}):${summary}\n\n${result.content}${cut}${more}`)
        },
      },
      async execute(args, exec): Promise<DocumentValue> {
        const toolName = name('read_document')
        const knowledgeId = requiredString(args, 'knowledge_id', toolName)
        const page = boundedIntArg(args, 'page', 1, 10_000)
        const pageSize = boundedIntArg(args, 'page_size', 20, 100)
        const [result, metadata] = await Promise.all([
          client.listChunks({ knowledgeId, page, pageSize }, exec.signal),
          // Only page 1 is worth the extra call and the summary's tokens: by
          // page 2 the model has already seen what this document is. Metadata
          // is context, not the payload, so a deployment that cannot serve it
          // must still hand over the text.
          page === 1
            ? client.getDocument(knowledgeId, exec.signal).catch(() => ({}) as DocumentRecord)
            : Promise.resolve({} as DocumentRecord),
        ])
        const ordered = [...result.chunks].sort(orderByChunkIndex)
        const joined = ordered.map(chunk => typeof chunk.content === 'string' ? chunk.content : '').join('\n\n')
        const clipped = clip(joined, config.maxChunkChars * Math.max(1, Math.min(ordered.length, 10)))
        const title = typeof metadata.title === 'string' && metadata.title.trim() !== ''
          ? metadata.title.trim()
          : typeof metadata.file_name === 'string' ? metadata.file_name.trim() : ''
        return {
          knowledge_id: knowledgeId,
          title,
          summary: clip(typeof metadata.description === 'string' ? metadata.description.trim() : '', config.maxChunkChars).text,
          page: result.page,
          page_size: result.pageSize,
          total_chunks: result.total,
          returned_chunks: ordered.length,
          has_more: result.page * result.pageSize < result.total,
          truncated: clipped.truncated,
          content: clipped.text,
        }
      },
    })
  }

  if (config.tools.ask) {
    const pipeline = config.agentId === undefined ? 'RAG' : 'agent'
    definitions.push({
      name: name('ask'),
      description: `Ask WeKnora a question and get its own composed answer with citations (${pipeline} pipeline runs `
        + 'server-side: query rewriting, retrieval, reranking and summarization). Reserve it for broad or '
        + 'synthesis questions whose answer spans many documents, where retrieving passages yourself would take '
        + `several rounds. For anything you can answer from specific passages, prefer ${name('search')}: it is far `
        + 'faster and leaves you the evidence to reason over rather than another model\'s conclusion.',
      parameters: {
        type: 'object',
        properties: {
          query: { type: 'string', description: 'The question to answer.' },
          knowledge_base_ids: { ...STRING_ARRAY, description: 'Restrict retrieval to these knowledge base ids.' },
          agent_id: { type: 'string', description: 'Custom agent id; selects the server-side ReAct pipeline.' },
          session_id: { type: 'string', description: 'Continue an earlier WeKnora session instead of starting one.' },
          web_search: { type: 'boolean', description: 'Let WeKnora also search the web, when its deployment allows it.' },
        },
        required: ['query'],
        additionalProperties: false,
      },
      timeoutMs: config.chatTimeoutMs,
      output: {
        schema: {
          type: 'object',
          properties: {
            answer: { type: 'string' },
            session_id: { type: 'string' },
            pipeline: { type: 'string', enum: ['rag', 'agent'] },
            tool_calls: STRING_ARRAY,
            references: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  knowledge_id: { type: 'string' },
                  document: { type: 'string' },
                  chunk_index: { type: 'integer' },
                  content: { type: 'string' },
                },
                required: ['knowledge_id', 'document', 'chunk_index', 'content'],
                additionalProperties: false,
              },
            },
          },
          required: ['answer', 'session_id', 'pipeline', 'tool_calls', 'references'],
          additionalProperties: false,
        },
        render: (_args, value) => {
          const result = value as AskValue
          const parts: string[] = []
          parts.push(result.answer === ''
            ? 'WeKnora returned an empty answer. Retry with a more specific question, or retrieve passages instead.'
            : result.answer)
          if (result.references.length > 0) {
            const cited = result.references.map((reference, index) =>
              `[${index + 1}] ${reference.document} · chunk ${reference.chunk_index} · knowledge_id: ${reference.knowledge_id}`)
            parts.push(`Citations:\n${cited.join('\n')}`)
          }
          if (result.tool_calls.length > 0) parts.push(`WeKnora tools used: ${result.tool_calls.join(', ')}`)
          parts.push(`WeKnora session: ${result.session_id} (pass session_id to ask a follow-up in context)`)
          return text(parts.join('\n\n'))
        },
      },
      async execute(args, exec): Promise<AskValue> {
        const toolName = name('ask')
        const query = requiredString(args, 'query', toolName)
        const requested = stringArrayArg(args, 'knowledge_base_ids')
        const knowledgeBaseIds = requested.length > 0 ? requested : config.knowledgeBaseIds
        const agentId = optionalStringArg(args, 'agent_id') ?? config.agentId
        const webSearch = argRecord(args)['web_search'] === true
        const sessionId = optionalStringArg(args, 'session_id')
          ?? await client.createSession(`dsh: ${clip(query, 60).text}`, exec.signal)
        const streamed = await client.ask({ sessionId, query, knowledgeBaseIds, agentId, webSearch }, exec.signal)
        return {
          answer: streamed.answer.trim(),
          session_id: streamed.sessionId,
          pipeline: agentId === undefined ? 'rag' : 'agent',
          tool_calls: streamed.toolCalls,
          references: streamed.references.slice(0, config.maxResults).map(reference => ({
            knowledge_id: typeof reference.knowledge_id === 'string' ? reference.knowledge_id : '',
            document: documentLabel(reference),
            chunk_index: typeof reference.chunk_index === 'number' ? reference.chunk_index : -1,
            content: clip(typeof reference.content === 'string' ? reference.content : '', config.maxChunkChars).text,
          })),
        }
      },
    })
  }

  return definitions
}

/** Storage order, so a reassembled document reads top to bottom. */
function orderByChunkIndex(left: ChunkRecord, right: ChunkRecord): number {
  const a = typeof left.chunk_index === 'number' ? left.chunk_index : 0
  const b = typeof right.chunk_index === 'number' ? right.chunk_index : 0
  return a - b
}
