/** The model-facing tools this plugin contributes to opencode.
 *
 * Ported from `dsh-weknora` (WeKnora's DeepSeek Harness plugin): the same four
 * tools, the same scope resolution and clipping behaviour, rendered into
 * opencode's tool registration shape — Zod argument schemas, an abort signal
 * from the agent, and an `output` + `metadata` result envelope.
 */

import { tool } from '@opencode-ai/plugin'

import type { ChunkRecord, DocumentRecord, SearchResult, WeknoraClient } from './client.ts'
import type { ResolvedConfig } from './config.ts'
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

/** Storage order, so a reassembled document reads top to bottom. */
function orderByChunkIndex(left: ChunkRecord, right: ChunkRecord): number {
  const a = typeof left.chunk_index === 'number' ? left.chunk_index : 0
  const b = typeof right.chunk_index === 'number' ? right.chunk_index : 0
  return a - b
}

/** Assemble the four tool definitions for one configured deployment. */
export function createTools(client: WeknoraClient, config: ResolvedConfig): Record<string, ReturnType<typeof tool>> {
  const name = (suffix: string): string => `${config.toolPrefix}_${suffix}`
  const scopeNote = config.knowledgeBaseIds.length > 0
    ? ` Searches knowledge base(s) ${config.knowledgeBaseIds.join(', ')} unless you name others.`
    : ' Searches every knowledge base this credential can see unless you narrow it with knowledge_base_ids.'

  // An unscoped call reaches WeKnora as a refusal on the retrieval route and as
  // an answer grounded in nothing on the RAG route, so an unconfigured
  // deployment resolves the full visible set once and reuses it. Making the
  // model pick a scope first is worse: knowledge bases are often named too
  // poorly to choose between, and fanning out across them is cheap while they
  // share a vector store.
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

  const tools: Record<string, ReturnType<typeof tool>> = {}

  if (config.tools.listKnowledgeBases) {
    tools[name('list_knowledge_bases')] = tool({
      description: 'List the WeKnora knowledge bases this deployment can retrieve from, with their ids. '
        + `${name('search')} already spans them all, so reach for this only to report what is available or to `
        + 'narrow a later search to one of them.',
      args: {},
      async execute(_args, context) {
        const bases = await client.listKnowledgeBases(context.abort)
        const value: KnowledgeBasesValue = {
          count: bases.length,
          knowledge_bases: bases.map(kb => ({
            id: typeof kb.id === 'string' ? kb.id : '',
            name: typeof kb.name === 'string' ? kb.name : '',
            description: typeof kb.description === 'string' ? kb.description : '',
          })),
        }
        if (value.count === 0) {
          return { title: 'WeKnora knowledge bases', output: 'No knowledge base is available to this WeKnora credential.', metadata: value }
        }
        const lines = value.knowledge_bases.map(kb => {
          const description = kb.description === '' ? '' : ` — ${kb.description}`
          return `- ${kb.name} (id: ${kb.id})${description}`
        })
        return {
          title: 'WeKnora knowledge bases',
          output: `${value.count} knowledge base(s):\n${lines.join('\n')}`,
          metadata: value,
        }
      },
    })
  }

  if (config.tools.search) {
    tools[name('search')] = tool({
      description: 'Search WeKnora knowledge bases and return the matching passages verbatim (hybrid vector + keyword '
        + 'retrieval, no model summarization). Use this to ground an answer in the organization\'s own documents, and '
        + 'also to locate a document by name — a query that reads like a title additionally returns the documents it '
        + 'names.' + scopeNote
        + ` Every hit carries a knowledge_id you can pass to ${name('read_document')} for the full document.`,
      args: {
        query: tool.schema.string().describe('Natural-language query; a full question retrieves better than keywords.'),
        knowledge_base_ids: tool.schema.array(tool.schema.string()).optional()
          .describe('Restrict the search to these knowledge base ids.'),
        knowledge_ids: tool.schema.array(tool.schema.string()).optional()
          .describe('Restrict the search to these document ids.'),
        max_results: tool.schema.number().int().optional()
          .describe(`Maximum passages to return (default ${config.maxResults}).`),
      },
      async execute(args, context) {
        const toolName = name('search')
        const query = args.query.trim()
        const knowledgeIds = args.knowledge_ids ?? []
        const knowledgeBaseIds = await resolveScope(args.knowledge_base_ids ?? [], knowledgeIds, context.abort)
        // Fail here rather than let WeKnora answer 400: the model can act on a
        // message naming the argument to supply, not on a transport error.
        if (knowledgeBaseIds.length === 0 && knowledgeIds.length === 0) {
          throw new Error(`${toolName}: this WeKnora credential can see no knowledge base, so there is `
            + 'nothing to search. Check the deployment\'s API key scope.')
        }
        const requested = args.max_results
        const limit = typeof requested === 'number' && Number.isFinite(requested) && requested > 0
          ? Math.min(Math.floor(requested), config.maxResults)
          : config.maxResults
        const [hits, named] = await Promise.all([
          client.search({ query, knowledgeBaseIds, knowledgeIds }, context.abort),
          // By-name matching is an enrichment, and older WeKnora builds may not
          // route it. Losing it must not cost the model its passages.
          client.findDocuments({ keyword: query, limit }, context.abort).catch(() => []),
        ])
        const results = hits.slice(0, limit).map((hit, index) => projectHit(hit, index + 1, config.maxChunkChars))
        const value: SearchValue = {
          query,
          knowledge_base_ids: knowledgeBaseIds,
          count: results.length,
          results,
          documents: projectNamedDocuments(named, results, knowledgeBaseIds, knowledgeIds),
        }
        const namedText = value.documents.length === 0
          ? ''
          : `\n\nDocuments named "${value.query}" (read them with ${name('read_document')}):\n`
            + value.documents.map(document =>
              `- ${document.title} · knowledge_id: ${document.knowledge_id} · in ${document.knowledge_base}`).join('\n')
        let output: string
        if (value.count === 0) {
          output = namedText === ''
            ? `Nothing in WeKnora matched "${value.query}" (searched: ${describeScope(value.knowledge_base_ids)}). `
              + 'Try a differently worded query, or widen the knowledge base scope.'
            : `No passage matched "${value.query}", but its name matches document(s).${namedText}`
        } else {
          const blocks = value.results.map(hit =>
            `[${hit.rank}] ${hit.document} · score ${formatScore(hit.score)} · chunk ${hit.chunk_index} `
            + `· knowledge_id: ${hit.knowledge_id}\n${hit.content}${hit.truncated ? '\n(passage truncated)' : ''}`)
          output = `${value.count} passage(s) for "${value.query}" `
            + `(searched: ${describeScope(value.knowledge_base_ids)}):\n\n${blocks.join('\n\n')}${namedText}`
        }
        return { title: `WeKnora search: ${clip(query, 60).text}`, output, metadata: value }
      },
    })
  }

  if (config.tools.readDocument) {
    tools[name('read_document')] = tool({
      description: 'Read a WeKnora document\'s stored passages in order, reassembled into text. '
        + `Use it after ${name('search')} when one passage is not enough context; page through long documents.`,
      args: {
        knowledge_id: tool.schema.string().describe('Document id, as returned in a search hit.'),
        page: tool.schema.number().int().optional().describe('Page of passages, starting at 1.'),
        page_size: tool.schema.number().int().optional().describe('Passages per page (max 100).'),
      },
      async execute(args, context) {
        const knowledgeId = args.knowledge_id.trim()
        const page = typeof args.page === 'number' && args.page > 0 ? Math.min(Math.floor(args.page), 10_000) : 1
        const pageSize = typeof args.page_size === 'number' && args.page_size > 0
          ? Math.min(Math.floor(args.page_size), 100)
          : 20
        const [result, metadata] = await Promise.all([
          client.listChunks({ knowledgeId, page, pageSize }, context.abort),
          // Only page 1 is worth the extra call and the summary's tokens: by
          // page 2 the model has already seen what this document is. Metadata
          // is context, not the payload, so a deployment that cannot serve it
          // must still hand over the text.
          page === 1
            ? client.getDocument(knowledgeId, context.abort).catch(() => ({}) as DocumentRecord)
            : Promise.resolve({} as DocumentRecord),
        ])
        const ordered = [...result.chunks].sort(orderByChunkIndex)
        const joined = ordered.map(chunk => typeof chunk.content === 'string' ? chunk.content : '').join('\n\n')
        const clipped = clip(joined, config.maxChunkChars * Math.max(1, Math.min(ordered.length, 10)))
        const title = typeof metadata.title === 'string' && metadata.title.trim() !== ''
          ? metadata.title.trim()
          : typeof metadata.file_name === 'string' ? metadata.file_name.trim() : ''
        const value: DocumentValue = {
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
        const label = value.title === '' ? value.knowledge_id : `${value.title} (${value.knowledge_id})`
        let output: string
        if (value.returned_chunks === 0) {
          output = `Document ${label} has no passage on page ${value.page} (${value.total_chunks} passage(s) in total).`
        } else {
          // The summary lets the model judge a long document from page 1
          // instead of paging blindly to find out what it is holding.
          const summary = value.summary === '' ? '' : `\n\nSummary: ${value.summary}`
          const more = value.has_more ? `\n\n(more passages available: request page ${value.page + 1})` : ''
          const cut = value.truncated ? '\n(content truncated)' : ''
          output = `Document ${label}, passages ${value.returned_chunks} of ${value.total_chunks} `
            + `(page ${value.page}):${summary}\n\n${value.content}${cut}${more}`
        }
        return { title: `WeKnora document: ${label}`, output, metadata: value }
      },
    })
  }

  if (config.tools.ask) {
    const pipeline = config.agentId === undefined ? 'RAG' : 'agent'
    tools[name('ask')] = tool({
      description: `Ask WeKnora a question and get its own composed answer with citations (${pipeline} pipeline runs `
        + 'server-side: query rewriting, retrieval, reranking and summarization). Reserve it for broad or '
        + 'synthesis questions whose answer spans many documents, where retrieving passages yourself would take '
        + `several rounds. For anything you can answer from specific passages, prefer ${name('search')}: it is far `
        + 'faster and leaves you the evidence to reason over rather than another model\'s conclusion.',
      args: {
        query: tool.schema.string().describe('The question to answer.'),
        knowledge_base_ids: tool.schema.array(tool.schema.string()).optional()
          .describe('Restrict retrieval to these knowledge base ids.'),
        agent_id: tool.schema.string().optional().describe('Custom agent id; selects the server-side ReAct pipeline.'),
        session_id: tool.schema.string().optional().describe('Continue an earlier WeKnora session instead of starting one.'),
        web_search: tool.schema.boolean().optional().describe('Let WeKnora also search the web, when its deployment allows it.'),
      },
      async execute(args, context) {
        const toolName = name('ask')
        const query = args.query.trim()
        const requested = args.knowledge_base_ids ?? []
        const agentId = (typeof args.agent_id === 'string' && args.agent_id.trim() !== '' ? args.agent_id.trim() : undefined)
          ?? config.agentId
        // A custom agent resolves its own scope server-side from its
        // KBSelectionMode, and ids sent here would override that as an explicit
        // mention. The RAG pipeline has no such default: it retrieves only what
        // the request names, and answers from nothing when it names nothing.
        const knowledgeBaseIds = agentId === undefined
          ? await resolveScope(requested, [], context.abort)
          : requested.length > 0 ? requested : config.knowledgeBaseIds
        if (agentId === undefined && knowledgeBaseIds.length === 0) {
          throw new Error(`${toolName}: this WeKnora credential can see no knowledge base, so there is `
            + 'nothing to answer from. Check the deployment\'s API key scope.')
        }
        const sessionId = (typeof args.session_id === 'string' && args.session_id.trim() !== '' ? args.session_id.trim() : undefined)
          ?? await client.createSession(`opencode: ${clip(query, 60).text}`, context.abort)
        const streamed = await client.ask({ sessionId, query, knowledgeBaseIds, agentId, webSearch: args.web_search === true }, context.abort)
        const value: AskValue = {
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
        const parts: string[] = []
        parts.push(value.answer === ''
          ? 'WeKnora returned an empty answer. Retry with a more specific question, or retrieve passages instead.'
          : value.answer)
        if (value.references.length > 0) {
          const cited = value.references.map((reference, index) =>
            `[${index + 1}] ${reference.document} · chunk ${reference.chunk_index} · knowledge_id: ${reference.knowledge_id}`)
          parts.push(`Citations:\n${cited.join('\n')}`)
        }
        if (value.tool_calls.length > 0) parts.push(`WeKnora tools used: ${value.tool_calls.join(', ')}`)
        parts.push(`WeKnora session: ${value.session_id} (pass session_id to ask a follow-up in context)`)
        return { title: `WeKnora answer: ${clip(query, 60).text}`, output: parts.join('\n\n'), metadata: value }
      },
    })
  }

  return tools
}
