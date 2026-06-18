import assert from 'node:assert/strict'
import test from 'node:test'

import {
  createChatMarkdownRenderer,
  preprocessMathDelimiters,
  renderChatMarkdown,
  replaceIncompleteImageWithPlaceholder,
  stripTrailingStreamingHorizontalRule,
} from './chatMarkdownRenderer.ts'
import {
  collapseStandaloneCitationParagraphs,
  joinCitationTagsToPreviousLine,
  resolveCitationChunkId,
  stripIncompleteCitationTag,
} from './citationMarkdown.ts'

const SAMPLE_DOC = 'example-report.docx'
const SAMPLE_CHUNK_A = '00000001-0000-4000-8000-000000000001'
const SAMPLE_CHUNK_B = '00000002-0000-4000-8000-000000000002'
const SAMPLE_CHUNK_C = '00000003-0000-4000-8000-000000000003'
const SAMPLE_CHUNK_PRESERVE = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'

test('preprocessMathDelimiters converts escaped math delimiters for marked-katex', () => {
  assert.equal(
    preprocessMathDelimiters('inline \\(a+b\\) and block \\[x^2\\]'),
    'inline $a+b$ and block $$x^2$$',
  )
})

test('replaceIncompleteImageWithPlaceholder hides an unfinished streaming image', () => {
  assert.equal(
    replaceIncompleteImageWithPlaceholder('before ![chart](local://bucket/path'),
    'before <span class="streaming-image-loading"><span class="streaming-image-loading__skeleton"></span></span>',
  )
})

test('stripIncompleteCitationTag hides only an unfinished streaming citation tail', () => {
  const prefix = 'Source '
  const complete = '<kb doc="2.jpg" chunk_id="3c67efd5-f2ff-4e26-9032-9e44e6861178" />'

  for (const partial of ['<', '<k', '<kb', '<kb ', '<kb doc="2.jpg"', '<w', '<we', '<web url="https://example.com"']) {
    assert.equal(stripIncompleteCitationTag(prefix + partial), prefix)
  }

  assert.equal(stripIncompleteCitationTag(prefix + complete), prefix + complete)
  assert.equal(stripIncompleteCitationTag('Value < 5'), 'Value < 5')
})

test('stripTrailingStreamingHorizontalRule hides an ambiguous trailing rule only mid-stream', () => {
  for (const rule of ['---', '* * *', '___']) {
    assert.equal(stripTrailingStreamingHorizontalRule(`- item\n\n${rule}`), '- item\n\n')
  }
  assert.equal(stripTrailingStreamingHorizontalRule('- item\n\n---\nnext'), '- item\n\n---\nnext')

  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
  }
  const source = '- item\n\n---'
  assert.doesNotMatch(renderChatMarkdown(source, { ...options, streaming: true }), /<hr>/)
  assert.match(renderChatMarkdown(source, { ...options, streaming: false }), /<hr>/)
})

test('renderChatMarkdown preserves citations, math, and sanitized output through one shared pipeline', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, title, text }) =>
      `<img src="${href}" alt="${text}" title="${title || ''}" class="markdown-image">`,
    isValidImageUrl: (href) => href.startsWith('https://'),
  })

  const html = renderChatMarkdown(
    [
      'See <kb doc="sample-product-guide.pdf" chunk_id="chunk-1" kb_id="kb-1"/>',
      '',
      'Formula: \\(E=mc^2\\)',
      '',
      '| A | B |',
      '| --- | --- |',
      '| 1 | 2 |',
      '',
      '![ok](https://example.com/a.png "图")',
      '',
      '![bad](javascript:alert(1))',
    ].join('\n'),
    {
      renderer,
      escapeMarkdown: (text) => text,
      sanitizeHtml: (html) => html.replace(/javascript:alert\(1\)/g, ''),
    },
  )

  assert.match(html, /class="citation citation-kb"/)
  assert.match(html, /data-chunk-id="chunk-1"/)
  assert.match(html, /katex/)
  assert.match(html, /<div class="chat-markdown-table"><table>/)
  assert.match(html, /<img src="https:\/\/example\.com\/a\.png"/)
  assert.doesNotMatch(html, /javascript:alert/)
})

test('resolveCitationChunkId maps context index to retrieval chunk UUID', () => {
  const refs = [
    { id: 'uuid-chunk-1', knowledge_title: 'Doc A', chunk_type: 'faq' },
    { id: 'uuid-chunk-2', knowledge_title: 'FAQ TEST - FAQ', chunk_type: 'faq' },
  ]

  assert.equal(
    resolveCitationChunkId('2', { doc: 'FAQ TEST - FAQ' }, refs),
    'uuid-chunk-2',
  )
  assert.equal(
    resolveCitationChunkId('FAQ-2', { doc: 'FAQ TEST - FAQ' }, refs),
    'uuid-chunk-2',
  )
  assert.equal(
    resolveCitationChunkId('uuid-chunk-1', { doc: 'Doc A' }, refs),
    'uuid-chunk-1',
  )
})

test('renderChatMarkdown preserves chunk UUIDs when escapeMarkdown strips UUIDs from prose', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
    isValidImageUrl: () => true,
  })
  const chunkId = SAMPLE_CHUNK_PRESERVE
  const stripUuids = (text: string) => text.replace(
    /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi,
    '',
  )

  const html = renderChatMarkdown(
    `Sample text <kb doc="sample-topic.pdf" chunk_id="${chunkId}" />`,
    {
      renderer,
      escapeMarkdown: stripUuids,
      sanitizeHtml: (html) => html,
    },
  )

  assert.match(html, /class="citation citation-kb"/)
  assert.match(html, new RegExp(`data-chunk-id="${chunkId}"`))
})

test('renderChatMarkdown resolves indexed chunk_id when knowledge references are provided', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
    isValidImageUrl: () => true,
  })

  const html = renderChatMarkdown(
    'See <kb doc="Sample FAQ" chunk_id="2" />',
    {
      renderer,
      escapeMarkdown: (text) => text,
      sanitizeHtml: (html) => html,
      knowledgeReferences: [
        { id: 'resolved-chunk-id', knowledge_title: 'Sample FAQ', chunk_type: 'faq' },
        { id: 'other-chunk', knowledge_title: 'Other FAQ', chunk_type: 'faq' },
      ],
    },
  )

  assert.match(html, /data-chunk-id="resolved-chunk-id"/)
  assert.doesNotMatch(html, /data-chunk-id="2"/)
})

test('joinCitationTagsToPreviousLine removes blank lines before citation tags', () => {
  const input = 'Setup is complete.\n\n<kb doc="faq.pdf" chunk_id="1" />'
  assert.equal(joinCitationTagsToPreviousLine(input), 'Setup is complete. <kb doc="faq.pdf" chunk_id="1" />')
})

test('joinCitationTagsToPreviousLine inlines consecutive citation tags across single newlines', () => {
  const tag1 = `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_A}" />`
  const tag2 = `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_B}" />`
  const tag3 = `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_C}" />`
  const input = `${tag1}\n${tag2}\n${tag3}`
  assert.equal(joinCitationTagsToPreviousLine(input), `${tag1} ${tag2} ${tag3}`)
})

test('renderChatMarkdown inlines consecutive citation tags across newlines', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
    isValidImageUrl: () => true,
  })
  const html = renderChatMarkdown(
    [
      `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_A}" />`,
      `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_B}" />`,
      `<kb doc="${SAMPLE_DOC}" chunk_id="${SAMPLE_CHUNK_C}" />`,
    ].join('\n'),
    {
      renderer,
      escapeMarkdown: (text) => text,
      sanitizeHtml: (html) => html,
    },
  )

  assert.equal((html.match(/citation-kb/g) || []).length, 3)
  assert.doesNotMatch(html, /<\/p>\s*<p>\s*<span class="citation citation-kb"/)
})

test('joinCitationTagsToPreviousLine appends an indented citation to the preceding list item', () => {
  const tag = '<kb doc="阅读之星全国青少年阅读风采展示活动.pdf" chunk_id="chunk-1" />'
  const input = [
    '#### 5️⃣ 阅读之星培养基地',
    '- 每个组别冠亚季军及前十强所在的学校，将获得 **"阅读之星培养基地"** 奖牌',
    '',
    `  ${tag}`,
  ].join('\n')
  assert.equal(
    joinCitationTagsToPreviousLine(input),
    [
      '#### 5️⃣ 阅读之星培养基地',
      `- 每个组别冠亚季军及前十强所在的学校，将获得 **"阅读之星培养基地"** 奖牌 ${tag}`,
    ].join('\n'),
  )
})

test('renderChatMarkdown renders a citation after a list item inline in that item', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
    isValidImageUrl: () => true,
  })
  const tag = '<kb doc="阅读之星全国青少年阅读风采展示活动.pdf" chunk_id="chunk-1" />'
  const html = renderChatMarkdown(`- 培养基地奖牌\n\n  ${tag}`, {
    renderer,
    escapeMarkdown: (text) => text,
    sanitizeHtml: (value) => value,
  })

  assert.match(html, /<li>培养基地奖牌 <span class="citation citation-kb"/)
  assert.doesNotMatch(html, /<\/ul>\s*<p>\s*<span class="citation citation-kb"/)
})

test('joinCitationTagsToPreviousLine does not merge citations onto fenced code closing delimiter', () => {
  const tag = '<kb doc="guide.pdf" chunk_id="1" />'
  const input = '```bash\nunzip setup.zip\n```\n\n' + tag
  assert.equal(joinCitationTagsToPreviousLine(input), '```bash\nunzip setup.zip\n```\n\n' + tag)
})

test('renderChatMarkdown keeps fenced code blocks closed when citations follow', () => {
  const renderer = createChatMarkdownRenderer({
    imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
    isValidImageUrl: () => true,
  })
  const tag = '<kb doc="guide.pdf" chunk_id="1" />'
  const html = renderChatMarkdown(
    ['```bash', 'unzip setup.zip', '```', '', tag, '', '#### Next step'].join('\n'),
    {
      renderer,
      escapeMarkdown: (text) => text,
      sanitizeHtml: (html) => html,
    },
  )

  assert.doesNotMatch(html, /#### Next step/)
  assert.match(html, /<h4>Next step<\/h4>/)
  assert.equal((html.match(/<pre>/g) || []).length, 1)
})

test('collapseStandaloneCitationParagraphs merges citations across empty paragraphs', () => {
  const html = '<p>Steps:</p><p></p><p><span class="citation citation-kb" data-chunk-id="x">doc</span></p>'
  const out = collapseStandaloneCitationParagraphs(html)
  assert.match(out, /Steps:.*citation-kb/s)
  assert.doesNotMatch(out, /<p><\/p>/)
})
