/**
 * A deterministic OpenAI-compatible model server for the end-to-end run. dsh's
 * `llm-pi-ai` adapter talks to it as an ordinary `openai-completions` gateway,
 * which lets the real harness agent loop drive real tool calls with no API key
 * and no nondeterminism.
 *
 * The scripted policy is: search the knowledge base, read the document a hit
 * points at, then answer using only what the tools returned. Every request is
 * recorded so the test can assert which tool schemas the model was offered.
 *
 * With `listTool` the policy gains the discovery step an unscoped deployment
 * forces: list the knowledge bases first and carry an id into the search.
 */

import { createServer } from 'node:http'

/** Emit one OpenAI streaming chunk. */
function chunk(response, model, delta, finishReason = null) {
  const payload = {
    id: 'chatcmpl-weknora-e2e',
    object: 'chat.completion.chunk',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  }
  response.write(`data: ${JSON.stringify(payload)}\n\n`)
}

function streamToolCall(response, model, name, args) {
  chunk(response, model, {
    role: 'assistant',
    content: null,
    tool_calls: [{ index: 0, id: `call_${name}`, type: 'function', function: { name, arguments: '' } }],
  })
  chunk(response, model, {
    tool_calls: [{ index: 0, function: { arguments: JSON.stringify(args) } }],
  })
  chunk(response, model, {}, 'tool_calls')
  response.write('data: [DONE]\n\n')
  response.end()
}

function streamText(response, model, text) {
  chunk(response, model, { role: 'assistant', content: '' })
  for (const piece of text.match(/.{1,40}/gs) ?? []) chunk(response, model, { content: piece })
  chunk(response, model, {}, 'stop')
  response.write('data: [DONE]\n\n')
  response.end()
}

/** The user's question, taken from the first user message in the request. */
function userQuestion(messages) {
  const first = messages.find(message => message.role === 'user')
  if (first === undefined) return ''
  return typeof first.content === 'string'
    ? first.content
    : (first.content ?? []).map(part => part.text ?? '').join(' ')
}

function toolMessages(messages) {
  return messages.filter(message => message.role === 'tool')
    .map(message => typeof message.content === 'string'
      ? message.content
      : (message.content ?? []).map(part => part.text ?? '').join('\n'))
}

/** Which of our tools the assistant already called, in order. */
function calledTools(messages) {
  return messages
    .filter(message => message.role === 'assistant' && Array.isArray(message.tool_calls))
    .flatMap(message => message.tool_calls.map(call => call.function?.name))
    .filter(name => typeof name === 'string')
}

/**
 * Start the fake model.
 * @param options.searchTool - tool name to call first.
 * @param options.readTool - tool name to call with a knowledge_id from the first result.
 * @param options.listTool - when set, list knowledge bases first and scope the search to an id it returns.
 * @returns the base URL (with `/v1`), the recorded requests, and a close function.
 */
export async function startFakeModel(options = {}) {
  const searchTool = options.searchTool ?? 'weknora_search'
  const readTool = options.readTool ?? 'weknora_read_document'
  const listTool = options.listTool
  const requests = []

  const server = createServer((request, response) => {
    const chunks = []
    request.on('data', part => chunks.push(part))
    request.on('end', () => {
      const url = new URL(request.url, 'http://fake-model.invalid')
      if (!url.pathname.endsWith('/chat/completions')) {
        response.writeHead(404, { 'Content-Type': 'application/json' })
        response.end(JSON.stringify({ error: { message: `no route for ${url.pathname}` } }))
        return
      }
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      const messages = body.messages ?? []
      const offeredTools = (body.tools ?? []).map(tool => tool.function?.name).filter(Boolean)
      requests.push({
        model: body.model,
        offeredTools,
        roles: messages.map(message => message.role),
        toolResults: toolMessages(messages),
      })

      response.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        'Connection': 'keep-alive',
      })

      const already = calledTools(messages)
      const results = toolMessages(messages)

      if (listTool !== undefined && !already.includes(listTool)) {
        streamToolCall(response, body.model, listTool, {})
        return
      }

      if (!already.includes(searchTool)) {
        const scope = listTool === undefined
          ? {}
          : { knowledge_base_ids: [/\(id: ([^)\s]+)\)/.exec(results.join('\n'))?.[1]].filter(Boolean) }
        streamToolCall(response, body.model, searchTool, { query: userQuestion(messages), ...scope })
        return
      }

      if (!already.includes(readTool)) {
        const knowledgeId = /knowledge_id: (\S+)/.exec(results.join('\n'))?.[1]
        if (knowledgeId !== undefined) {
          streamToolCall(response, body.model, readTool, { knowledge_id: knowledgeId, page: 1, page_size: 5 })
          return
        }
      }

      // Answer strictly from what the tools returned, so the transcript proves
      // the retrieved bytes reached the model rather than a canned string.
      const grounding = results.join('\n').replace(/\s+/g, ' ').trim().slice(0, 400)
      streamText(response, body.model, `根据 WeKnora 知识库检索结果回答：${grounding}`)
    })
  })

  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  return {
    url: `http://127.0.0.1:${port}/v1`,
    requests,
    async close() {
      await new Promise(resolve => server.close(resolve))
    },
  }
}
