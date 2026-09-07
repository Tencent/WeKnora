import assert from 'node:assert/strict'
import { test } from 'node:test'

/**
 * Live integration against a real WeKnora deployment. Gated by environment:
 *   WEKNORA_LIVE_URL     e.g. http://localhost:8080
 *   WEKNORA_LIVE_API_KEY an API key with retrieve (+ chat for ask) access
 *   WEKNORA_LIVE_ASK=1   additionally run the (slow, model-backed) ask tool
 */

const url = process.env.WEKNORA_LIVE_URL
const apiKey = process.env.WEKNORA_LIVE_API_KEY

test('live: list, search and read against a deployment', { skip: url === undefined || apiKey === undefined }, async () => {
  const { WeknoraClient } = await import('../dist/client.js')
  const { resolveConfig } = await import('../dist/config.js')
  const { createTools } = await import('../dist/tools.js')

  const config = resolveConfig({ baseUrl: url, apiKey })
  const client = new WeknoraClient(config)
  const tools = createTools(client, config)
  const context = { abort: new AbortController().signal }

  const listed = await tools.weknora_list_knowledge_bases.execute({}, context)
  assert.ok(listed.metadata.count >= 1, 'the deployment has at least one knowledge base')
  const scope = listed.metadata.knowledge_bases.map((kb) => kb.id)

  // Any real corpus answers a query about itself; the sample document used in
  // this project's verification is titled "opencode-weknora integration probe".
  const searched = await tools.weknora_search.execute(
    { query: 'opencode weknora integration probe', knowledge_base_ids: scope }, context,
  )
  assert.ok(Array.isArray(searched.metadata.results))
  assert.ok(searched.metadata.count >= 1, `search returned ${searched.metadata.count} hits`)
  const hit = searched.metadata.results[0]
  assert.ok(hit.knowledge_id !== '')
  assert.ok(hit.content.length > 0)

  const read = await tools.weknora_read_document.execute({ knowledge_id: hit.knowledge_id }, context)
  assert.equal(read.metadata.knowledge_id, hit.knowledge_id)
  assert.ok(read.metadata.content.length > 0)

  if (process.env.WEKNORA_LIVE_ASK === '1') {
    const asked = await tools.weknora_ask.execute(
      { query: 'What does the integration probe document say about itself?', knowledge_base_ids: scope }, context,
    )
    assert.ok(asked.metadata.answer.length > 0)
    assert.ok(asked.metadata.session_id !== '')
  }
})
