import assert from 'node:assert/strict'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '../..')

async function loadCitationMarkdown() {
  const server = await createServer({
    root,
    configFile: false,
    optimizeDeps: {
      entries: [],
      noDiscovery: true,
    },
    server: { middlewareMode: true, hmr: false },
    resolve: {
      alias: {
        '@': resolve(root, 'src'),
      },
    },
  })
  try {
    return await server.ssrLoadModule('/src/utils/citationMarkdown.ts')
  } finally {
    await server.close()
  }
}

test('resolveCitationReferenceContent returns merged reference content for cited parent chunk', async () => {
  const { resolveCitationReferenceContent } = await loadCitationMarkdown()

  const content = resolveCitationReferenceContent(
    'parent-chunk',
    { doc: '眼底病临床诊治精要.mhtml', kbId: 'kb-1' },
    [
      {
        id: 'parent-chunk',
        knowledge_base_id: 'kb-1',
        knowledge_filename: '眼底病临床诊治精要.mhtml',
        content: '父 chunk + 邻居 chunk 合并后的真实上下文',
      },
    ],
  )

  assert.equal(content, '父 chunk + 邻居 chunk 合并后的真实上下文')
})

test('resolveCitationReferenceContent can match a cited neighbor sub_chunk_id', async () => {
  const { resolveCitationReferenceContent } = await loadCitationMarkdown()

  const content = resolveCitationReferenceContent(
    'neighbor-chunk',
    { doc: 'guide.pdf', kbId: 'kb-1' },
    [
      {
        id: 'parent-chunk',
        knowledge_base_id: 'kb-1',
        knowledge_filename: 'guide.pdf',
        sub_chunk_id: ['neighbor-chunk'],
        content: '包含 neighbor chunk 的合并上下文',
      },
    ],
  )

  assert.equal(content, '包含 neighbor chunk 的合并上下文')
})
