import assert from 'node:assert/strict'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '../..')

async function loadCitationPreview() {
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
    return await server.ssrLoadModule('/src/utils/citationPreview.ts')
  } finally {
    await server.close()
  }
}

test('renderCitationPreviewContent renders escaped html tables as table markup', async () => {
  const { renderCitationPreviewContent } = await loadCitationPreview()
  const html = renderCitationPreviewContent(
    '&lt;table&gt;&lt;tr&gt;&lt;th&gt;指标&lt;/th&gt;&lt;td&gt;NPDR&lt;/td&gt;&lt;/tr&gt;&lt;/table&gt;',
    { sanitizeHtml: (value) => value },
  )

  assert.match(html, /<table/)
  assert.match(html, /<td>NPDR<\/td>/)
  assert.doesNotMatch(html, /&lt;table/)
})

test('decodeEscapedHtmlBlocks leaves plain text comparisons escaped', async () => {
  const { decodeEscapedHtmlBlocks } = await loadCitationPreview()
  assert.equal(decodeEscapedHtmlBlocks('1 &lt; 2 and 3 &gt; 2'), '1 &lt; 2 and 3 &gt; 2')
})

test('renderCitationPreviewContent shows figure title from image alt under image', async () => {
  const { renderCitationPreviewContent } = await loadCitationPreview()
  const html = renderCitationPreviewContent(
    '![图 3-6-3 PDR 纤维增殖](local://10000/exports/example.jpg)\n\n图点评：纤维增殖代表此处曾发生新生血管。',
    { sanitizeHtml: (value) => value },
  )

  assert.match(html, /<figure class="citation-preview-figure">/)
  assert.match(html, /<figcaption class="citation-preview-figure__caption">图 3-6-3 PDR 纤维增殖<\/figcaption>/)
  assert.match(html, /图点评：纤维增殖代表此处曾发生新生血管。/)
})

test('renderCitationPreviewContent removes immediate duplicate figure title line', async () => {
  const { renderCitationPreviewContent } = await loadCitationPreview()
  const html = renderCitationPreviewContent(
    [
      '![图 3-5-1 DR 的 FFA 典型改变及静脉襻](local://10000/exports/example.jpg)',
      '',
      '图 3-5-1 DR 的 FFA 典型改变及静脉襻',
      '',
      'A. DR 的 FFA 典型改变。',
    ].join('\n'),
    { sanitizeHtml: (value) => value },
  )

  assert.equal((html.match(/图 3-5-1 DR 的 FFA 典型改变及静脉襻/g) || []).length, 2)
  assert.match(html, /<figcaption class="citation-preview-figure__caption">图 3-5-1 DR 的 FFA 典型改变及静脉襻<\/figcaption>/)
  assert.match(html, /A\. DR 的 FFA 典型改变。/)
})
