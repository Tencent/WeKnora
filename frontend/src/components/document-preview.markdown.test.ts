import assert from 'node:assert/strict'
import test from 'node:test'

import { Marked, Renderer } from 'marked'
import markedKatex from 'marked-katex-extension'

function createPreviewMarkdownRenderer(): Renderer {
  const renderer = new Renderer()
  renderer.image = ({ href, title, text }) => {
    const safeTitle = title ? ` title="${title}"` : ''
    return `<img src="${href}" alt="${text || ''}"${safeTitle} class="markdown-image" loading="lazy" decoding="async" fetchpriority="low" />`
  }
  return renderer
}

function renderPreviewMarkdown(source: string): string {
  const marked = new Marked({ breaks: true, gfm: true })
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
  return marked.parse(source, { renderer: createPreviewMarkdownRenderer() }) as string
}

test('preview Markdown keeps native lazy image attributes', () => {
  const html = renderPreviewMarkdown('![test](https://example.com/test.png)')

  assert.match(html, /loading="lazy"/)
  assert.match(html, /decoding="async"/)
  assert.match(html, /fetchpriority="low"/)
})

test('preview Markdown keeps the marked-katex extension when using an image renderer', () => {
  const html = renderPreviewMarkdown('公式 $E = mc^2$')

  assert.match(html, /katex/)
})
