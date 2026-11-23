import assert from 'node:assert/strict'
import test from 'node:test'

import { renderDocumentPreviewMarkdown } from './documentPreviewMarkdown.ts'
import {
  neutralizeRelativePreviewImages,
  PREVIEW_IMAGE_PLACEHOLDER,
  rewriteKnowledgeMarkdownImages,
  type RewriteKnowledgeMarkdownImagesDeps,
} from './markdownPreviewImages.ts'

const literalImage = '![example](images/example.png)'
const visibleImage = '![visible](images/visible.png)'

for (const { name, opening, body, closing, eol = '\n' } of [
  { name: 'tilde inside backtick fence', opening: '```md', body: ['~~~'], closing: '```' },
  { name: 'backtick inside tilde fence', opening: '~~~md', body: ['```'], closing: '~~~' },
  { name: 'shorter backtick fence', opening: '````md', body: ['```'], closing: '````' },
  { name: 'shorter tilde fence', opening: '~~~~md', body: ['~~~'], closing: '~~~~' },
  { name: 'backtick line with trailing text', opening: '```md', body: ['```not-a-close'], closing: '```' },
  { name: 'tilde line with trailing text', opening: '~~~md', body: ['~~~not-a-close'], closing: '~~~' },
  { name: 'several literal delimiters', opening: '````md', body: ['~~~', '```', '~~~~'], closing: '`````' },
  { name: 'indented longer closing fence', opening: '   ```md', body: ['~~~'], closing: '  `````  ' },
  { name: 'CRLF mixed fences', opening: '~~~md', body: ['```'], closing: '~~~~  ', eol: '\r\n' },
  { name: 'tab-suffixed backtick line', opening: '```md', body: ['``` \t'], closing: '```' },
  { name: 'tab-suffixed tilde line', opening: '~~~md', body: ['~~~\t'], closing: '~~~' },
  { name: 'ordinary backtick fence', opening: '```md', body: [], closing: '```' },
  { name: 'ordinary tilde fence', opening: '~~~md', body: [], closing: '~~~' },
]) {
  test(`preview preserves code and resolves the image after ${name}`, async () => {
    const code = [opening, ...body, literalImage, '<img src="images/example.png">', closing].join(eol)
    const markdown = `${code}${eol}${eol}${visibleImage}`
    assert.equal(
      neutralizeRelativePreviewImages(markdown),
      `${code}${eol}${eol}![visible](${PREVIEW_IMAGE_PLACEHOLDER})`,
    )

    const calls: string[] = []
    const rewritten = await rewriteKnowledgeMarkdownImages(markdown, 'doc', imageDeps(calls))
    assert.equal(rewritten.markdown, `${code}${eol}${eol}![visible](blob:visible)`)
    assert.deepEqual(rewritten.objectUrls, ['blob:visible'])
    assert.deepEqual(calls, ['knowledge:doc', 'list:kb:images:1', 'load:visible'])

    // The normal preview renderer agrees: literal images are escaped code,
    // and exactly one real image uses the resolved blob URL.
    const html = renderDocumentPreviewMarkdown(rewritten.markdown, value => value)
    assert.equal((html.match(/<img\b/g) || []).length, 1)
    assert.match(html, /<img src="blob:visible"/)
  })
}

for (const opening of ['```md', '~~~md', '````md\n```', '```md\n~~~', '```md\n```text']) {
  test(`unclosed fence ${JSON.stringify(opening)} leaves image examples alone`, async () => {
    const markdown = `${opening}\n${literalImage}\n<img src="images/example.png">`
    assert.equal(neutralizeRelativePreviewImages(markdown), markdown)
    const calls: string[] = []
    assert.deepEqual(await rewriteKnowledgeMarkdownImages(markdown, 'doc', imageDeps(calls)), {
      markdown, objectUrls: [],
    })
    assert.deepEqual(calls, [])
  })
}

test('backticks in an info string do not hide subsequent images', async () => {
  const prefix = '```not`a-fence\n\n'
  const markdown = prefix + visibleImage
  assert.equal(neutralizeRelativePreviewImages(markdown), prefix + `![visible](${PREVIEW_IMAGE_PLACEHOLDER})`)
  const calls: string[] = []
  const rewritten = await rewriteKnowledgeMarkdownImages(markdown, 'doc', imageDeps(calls))
  assert.equal(rewritten.markdown, prefix + '![visible](blob:visible)')
  assert.deepEqual(rewritten.objectUrls, ['blob:visible'])
})

function imageDeps(calls: string[]): RewriteKnowledgeMarkdownImagesDeps {
  return {
    getKnowledge: async (id) => {
      calls.push(`knowledge:${id}`)
      return { knowledgeBaseId: 'kb', folderPath: '' }
    },
    listFiles: async (kb, folder, page) => {
      calls.push(`list:${kb}:${folder}:${page}`)
      return {
        rows: ['example', 'visible'].map(id => ({ id, fileName: `${id}.png`, folderPath: 'images' })),
        total: 2,
      }
    },
    loadPreview: async (id) => {
      calls.push(`load:${id}`)
      return new Blob([id], { type: 'image/png' })
    },
    createObjectURL: () => 'blob:visible',
  }
}
