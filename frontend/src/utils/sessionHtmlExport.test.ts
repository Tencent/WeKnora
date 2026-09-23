import assert from 'node:assert/strict'
import test from 'node:test'

import { createChatMarkdownRenderer, renderChatMarkdown } from './chatMarkdownRenderer.ts'
import {
  buildExportFileName,
  buildSessionHtmlDocument,
  createExportRenderer,
  renderExportMessage,
} from './sessionHtmlExport.ts'
import { buildExportCss } from './sessionHtmlExportStyles.ts'

const LABELS = {
  sessionId: 'SESSION',
  exportedAt: 'EXPORTED',
  user: 'USER',
  assistant: 'ASSISTANT',
  attachments: 'ATTACHMENTS',
  references: 'REFERENCES',
}

/** DOMPurify has no DOM in this runner; identity keeps the real markup visible. */
const identity = (html: string): string => html

test('buildExportFileName sanitizes the title and stamps the time', () => {
  const name = buildExportFileName(
    'Q3/report: a<b>|c?"d"',
    'abcdef123456',
    new Date(2026, 8, 21, 14, 5),
  )
  assert.equal(name, 'Q3 report a b c d-20260921-1405.html')
  assert.doesNotMatch(name, /[\\/:*?"<>|]/)
})

test('buildExportFileName falls back to the session id when the title is unusable', () => {
  assert.equal(
    buildExportFileName('///', 'abcdef123456', new Date(2026, 0, 1, 0, 0)),
    'session-abcdef12-20260101-0000.html',
  )
  assert.equal(
    buildExportFileName('', '', new Date(2026, 0, 1, 0, 0)),
    'session-export-20260101-0000.html',
  )
})

test('user turns stay escaped plain text', () => {
  const html = renderExportMessage(
    { id: 'm1', role: 'user', content: '<script>alert(1)</script>\n第二行' },
    LABELS,
    { sanitizeHtml: identity },
  )
  assert.match(html, /class="export-message export-message--user"/)
  assert.match(html, /data-message-id="m1"/)
  assert.doesNotMatch(html, /<script>/)
  assert.match(html, /&lt;script&gt;/)
})

test('assistant turns reuse the chat markdown pipeline', () => {
  const html = renderExportMessage(
    {
      id: 'm2',
      role: 'assistant',
      content: [
        '| A | B |',
        '| --- | --- |',
        '| 1 | 2 |',
        '',
        '```python',
        'x = 1',
        '```',
        '',
        '引用一 <web url="https://example.com/a" title="标题"/>',
      ].join('\n'),
    },
    LABELS,
    { renderer: createExportRenderer(), sanitizeHtml: identity },
  )
  assert.match(html, /class="export-content"/)
  assert.match(html, /class="chat-markdown-table"/)
  assert.match(html, /<table>/)
  assert.match(html, /class="citation citation-web"/)
  // The tooltip is emitted inline and is only hidden by the export stylesheet.
  assert.match(html, /class="citation-tip"/)
})

test('roles the export does not carry and empty messages are skipped', () => {
  assert.equal(renderExportMessage({ role: 'tool', content: 'x' }, LABELS, { sanitizeHtml: identity }), '')
  assert.equal(renderExportMessage({ role: 'assistant', content: '   ' }, LABELS, { sanitizeHtml: identity }), '')
  assert.equal(renderExportMessage({ role: 'user', content: '' }, LABELS, { sanitizeHtml: identity }), '')
})

test('attachments and references are rendered with their labels', () => {
  const html = renderExportMessage(
    {
      id: 'm3',
      role: 'assistant',
      content: 'answer',
      attachments: [{ file_name: 'report.pdf' }],
      knowledge_references: [
        { knowledge_title: '文档A', metadata: { url: 'https://example.com/a' } },
        { knowledge_title: '文档B' },
      ],
    },
    LABELS,
    { renderer: createExportRenderer(), sanitizeHtml: identity },
  )
  assert.match(html, /ATTACHMENTS/)
  assert.match(html, /report\.pdf/)
  assert.match(html, /REFERENCES/)
  assert.match(html, /文档A/)
  assert.match(html, /文档B/)
  // `escapeHTML` also escapes slashes; browsers decode entities in attributes.
  assert.match(html, /<a href="https:&#x2F;&#x2F;example\.com&#x2F;a"/)
  assert.match(html, /target="_blank" rel="noopener noreferrer"/)
})

test('the document carries a doctype, CSP and escaped metadata', () => {
  const html = buildSessionHtmlDocument({
    title: 'My <Chat>',
    sessionId: 'sess-1',
    exportedAt: '2026-09-21T00:00:00.000Z',
    labels: LABELS,
    bodyHtml: '<section>body</section>',
    css: '.x{}',
    lang: 'zh-CN',
  })
  assert.match(html, /^<!DOCTYPE html>/)
  assert.match(html, /<html lang="zh-CN">/)
  assert.match(html, /http-equiv="Content-Security-Policy"/)
  assert.match(html, /default-src 'none'/)
  assert.match(html, /<title>My &lt;Chat&gt; - WeKnora<\/title>/)
  assert.match(html, /sess-1/)
  assert.match(html, /2026-09-21T00:00:00.000Z/)
  assert.match(html, /<section>body<\/section>/)
})

test('export css is standalone: no scoped selectors, no app asset urls', () => {
  const css = buildExportCss({ fontFamily: 'Arial', fontFamilyMono: 'monospace' })
  assert.doesNotMatch(css, /:deep\(/)
  assert.doesNotMatch(css, /\[data-v-/)
  assert.doesNotMatch(css, /mask-image:\s*url\('@\//)
  assert.doesNotMatch(css, /var\(--td-[a-z-]+,\s*$/)
  // Citation tooltips are emitted inline; hiding them is load-bearing.
  assert.match(css, /\.citation-tip\{display:none\}/)
  // Scrolling containers clip their overflow when printed.
  assert.match(css, /@media print/)
  assert.match(css, /overflow:visible!important/)
  // Light-theme tokens must be defined inside the document.
  assert.match(css, /--td-text-color-primary:/)
  assert.match(css, /--td-component-stroke:/)
})

test('export css keeps font stacks intact and only inlines optional payloads on demand', () => {
  const bare = buildExportCss({
    fontFamily: '-apple-system, "Segoe UI", sans-serif',
    fontFamilyMono: 'Menlo, monospace',
  })
  assert.match(bare, /--export-font-sans:-apple-system, "Segoe UI", sans-serif/)
  assert.doesNotMatch(bare, /color:#24292e/)
  assert.doesNotMatch(bare, /font-size:1\.21em/)

  const full = buildExportCss({
    fontFamily: 'sans',
    fontFamilyMono: 'mono',
    hljsCss: '.hljs{color:#24292e}',
    katexCss: '.katex{font-size:1.21em}',
  })
  assert.match(full, /\.hljs\{color:#24292e\}/)
  assert.match(full, /\.katex\{font-size:1\.21em\}/)
})

test('font stacks cannot break out of the style element', () => {
  const css = buildExportCss({
    fontFamily: 'Arial</style><script>alert(1)</script>',
    fontFamilyMono: 'mono',
  })
  assert.doesNotMatch(css, /<\/style>/)
  assert.doesNotMatch(css, /<script>/)
})

const SAMPLE = [
  '# H1',
  '',
  '段落 **加粗** *斜体* ~~删除~~ `code` [链接](https://example.com) '
    + '<kb chunk_id="00000001-0000-4000-8000-000000000001" knowledge_title="文档A"/> '
    + '<web url="https://example.com" title="网页"/> and [[wiki/page|Wiki]]',
  '',
  '**整段加粗小标题：**',
  '',
  '- 无序',
  '  - 嵌套',
  '1. 有序',
  '',
  '> 引用',
  '',
  '| 列A | 列B |',
  '| --- | --- |',
  '| a1 | b1 |',
  '',
  '```python',
  'def f(x):',
  '    return x + 1',
  '```',
  '',
  '```mermaid',
  'graph TD; A-->B;',
  '```',
  '',
  '行内 $E = mc^2$ 与块级：',
  '',
  '$$',
  '\\int_0^1 x^2 dx',
  '$$',
  '',
  '![受保护图片](resource://10000/exports/a.png)',
  '',
  '---',
].join('\n')

test('every class the renderer emits is styled by the export css or explicitly excluded', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text, title }) =>
      `<img src="${href}" alt="${text}" title="${title || ''}" class="markdown-image">`,
  })
  const html = renderChatMarkdown(SAMPLE, {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: identity,
    streaming: false,
    knowledgeReferences: [{
      id: '00000001-0000-4000-8000-000000000001',
      knowledge_title: '文档A',
    }],
  })

  const emitted = new Set<string>()
  for (const match of html.matchAll(/class="([^"]+)"/g)) {
    for (const name of match[1].split(/\s+/)) if (name) emitted.add(name)
  }
  // `createMermaidCodeRenderer` cannot be imported here (it pulls in a CSS
  // import), so the chrome it produces is listed explicitly.
  for (const name of [
    'chat-code-block', 'chat-code-block__header', 'chat-code-block__lang',
    'chat-code-block__actions', 'chat-code-block__copy', 'chat-code-block__copy-text',
    'chat-code-block__copy-icon', 'chat-code-block__pre',
    'chat-mermaid-block', 'chat-mermaid-block__header', 'chat-mermaid-block__badge',
    'chat-mermaid-block__actions', 'chat-mermaid-block__expand',
    'chat-mermaid-block__expand-icon', 'chat-mermaid-block__canvas',
  ]) emitted.add(name)

  // Covered by the stylesheet inlined on demand, not by the export stylesheet.
  const katexCssClasses = new Set([
    'katex', 'katex-display', 'katex-html', 'katex-mathml', 'base', 'mord', 'mop',
    'mrel', 'mspace', 'msupsub', 'mtight', 'vlist', 'vlist-r', 'vlist-s', 'vlist-t',
    'vlist-t2', 'pstrut', 'size3', 'reset-size6', 'sizing', 'strut', 'mathnormal',
    'large-op', 'op-symbol',
  ])
  // Removed from the DOM before the document is assembled (stripExportChrome).
  const removedClasses = new Set([
    'chat-code-block__actions', 'chat-code-block__copy', 'chat-code-block__copy-text',
    'chat-code-block__copy-icon', 'chat-mermaid-block__actions',
    'chat-mermaid-block__expand', 'chat-mermaid-block__expand-icon',
    'citation-tip', 'tip-title', 'tip-url', 'tip-loading',
    'citation-icon', 'citation-icon--web', 'citation-icon--book',
    'streaming-image-loading', 'streaming-image-loading__skeleton', 'stream-fade-tail',
  ])
  // Styled through a type selector (`img`) or purely informational.
  const passthroughClasses = new Set([
    'markdown-image', 'artifact-ref-image', 'hljs', 'language-python', 'language-mermaid',
    // `citation-wiki` rides on the same anchor as `wiki-content-link`.
    'citation-wiki',
  ])

  const css = buildExportCss({ fontFamily: 'sans', fontFamilyMono: 'mono' })
  const styled = new Set<string>()
  for (const match of css.matchAll(/\.([a-zA-Z][\w-]*)/g)) styled.add(match[1])

  const uncovered = [...emitted].filter((name) =>
    !styled.has(name)
    && !katexCssClasses.has(name)
    && !removedClasses.has(name)
    && !passthroughClasses.has(name))

  assert.deepEqual(
    uncovered,
    [],
    `renderer emitted classes the export does not style or explicitly exclude: ${uncovered.join(', ')}`,
  )

  // Guard the other direction: the business classes must genuinely be styled.
  for (const name of ['chat-markdown-table', 'md-strong-title', 'citation-kb', 'citation-web', 'citation-tip', 'wiki-content-link']) {
    assert.ok(styled.has(name), `export css is missing a rule for .${name}`)
  }
})
