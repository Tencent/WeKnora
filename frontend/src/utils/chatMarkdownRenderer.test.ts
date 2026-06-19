import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyStreamingTailFade,
  closeDanglingStreamingEmphasis,
  createChatMarkdownRenderer,
  markStandaloneStrongParagraphs,
  preprocessMathDelimiters,
  renderChatMarkdown,
  repairFlankingEmphasis,
  replaceIncompleteImageWithPlaceholder,
  stripTrailingStreamingHorizontalRule,
  stripTrailingStreamingListMarker,
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

/** Remove the streaming tail-fade wrapper so structural assertions stay focused. */
function stripFadeTail(html: string): string {
  return html.replace(/<span class="stream-fade-tail">([\s\S]*?)<\/span>/g, '$1')
}

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

test('closeDanglingStreamingEmphasis closes unfinished inline markers without false positives', () => {
  assert.equal(closeDanglingStreamingEmphasis('**平台地址：example.com'), '**平台地址：example.com**')
  assert.equal(closeDanglingStreamingEmphasis('前文 *斜体'), '前文 *斜体*')
  assert.equal(closeDanglingStreamingEmphasis('前文 ~~删除'), '前文 ~~删除~~')
  assert.equal(closeDanglingStreamingEmphasis('***又粗又斜'), '***又粗又斜***')
  assert.equal(closeDanglingStreamingEmphasis('运行 `npm run'), '运行 `npm run`')

  // A trailing marker run with no content after it is ambiguous (e.g. the start
  // of the next `**` in a bold list) and is hidden until content arrives, rather
  // than rendered as a literal `*`/`**` that flickers a frame later.
  assert.equal(closeDanglingStreamingEmphasis('4. **朝阳实验**\n5. *'), '4. **朝阳实验**\n5. ')
  assert.equal(closeDanglingStreamingEmphasis('5. **'), '5. ')
  assert.equal(closeDanglingStreamingEmphasis('3. **泳燃游泳*'), '3. **泳燃游泳**')

  // No dangling markers / structural markers must be left untouched.
  assert.equal(closeDanglingStreamingEmphasis('正文 **加粗** 收尾'), '正文 **加粗** 收尾')
  assert.equal(closeDanglingStreamingEmphasis('* 列表项一'), '* 列表项一')
  assert.equal(closeDanglingStreamingEmphasis('普通文本没有标记'), '普通文本没有标记')
  // Markers inside an open fenced code block are literal, not emphasis.
  assert.equal(
    closeDanglingStreamingEmphasis('```js\nconst x = **y'),
    '```js\nconst x = **y',
  )
})

test('renderChatMarkdown renders an unfinished bold line as bold immediately while streaming', () => {
  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
  }
  const partial = '**平台访问地址：chatbot.weixin.qq.com'
  // Streaming: optimistically bold so no raw `**` and no late layout jump.
  assert.match(
    stripFadeTail(renderChatMarkdown(partial, { ...options, streaming: true })),
    /<p class="md-strong-title"><strong>平台访问地址：chatbot\.weixin\.qq\.com<\/strong><\/p>/,
  )
  // Completed: a genuinely unterminated marker stays literal (we never invent
  // content for the final, authoritative render).
  assert.match(
    renderChatMarkdown(partial, { ...options, streaming: false }),
    /<p>\*\*平台访问地址/,
  )
})

test('repairFlankingEmphasis bolds punctuation-adjacent emphasis CommonMark would drop', () => {
  // Closing delimiter sits between punctuation and a letter -> CommonMark rejects
  // it; we convert exactly that blocked pattern to explicit HTML.
  assert.equal(repairFlankingEmphasis('**XBRL（语言）**是一种'), '<strong>XBRL（语言）</strong>是一种')
  assert.equal(repairFlankingEmphasis('**a)**b'), '<strong>a)</strong>b')
  assert.equal(repairFlankingEmphasis('~~删除）~~文字'), '<del>删除）</del>文字')
  assert.equal(repairFlankingEmphasis('*斜体）*文字'), '<em>斜体）</em>文字')

  // Cases marked already handles, literal markers, and code must be untouched.
  assert.equal(repairFlankingEmphasis('**中文**后面'), '**中文**后面')
  assert.equal(repairFlankingEmphasis('**中文）** 后面'), '**中文）** 后面')
  assert.equal(repairFlankingEmphasis('正文 **加粗** 收尾'), '正文 **加粗** 收尾')
  assert.equal(repairFlankingEmphasis('`**a)**b`'), '`**a)**b`')
  assert.equal(repairFlankingEmphasis('2 ** 3 ** 4'), '2 ** 3 ** 4')
  assert.equal(repairFlankingEmphasis('价格 3 * 5 * 7 元'), '价格 3 * 5 * 7 元')
})

test('renderChatMarkdown bolds punctuation-adjacent emphasis both mid-stream and when complete', () => {
  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
  }
  const text = '**XBRL（语言）**是一种标准'
  for (const streaming of [true, false]) {
    assert.match(
      stripFadeTail(renderChatMarkdown(text, { ...options, streaming })),
      /<strong>XBRL（语言）<\/strong>是一种标准/,
    )
  }
})

test('stripTrailingStreamingListMarker hides a content-less trailing list/underline marker', () => {
  assert.equal(stripTrailingStreamingListMarker('1. **AAAAA**\n   - '), '1. **AAAAA**\n')
  assert.equal(stripTrailingStreamingListMarker('文本\n1. '), '文本\n')
  assert.equal(stripTrailingStreamingListMarker('标题\n=='), '标题\n')
  // A lone trailing number is the start of an ordered marker (no `.` yet).
  assert.equal(stripTrailingStreamingListMarker('文本\n1'), '文本\n')
  // A marker with content after it is a real list item and must stay.
  assert.equal(stripTrailingStreamingListMarker('- 项目'), '- 项目')
  assert.equal(stripTrailingStreamingListMarker('1. 内容'), '1. 内容')
  // An in-sentence number must not be touched.
  assert.equal(stripTrailingStreamingListMarker('值是 1'), '值是 1')
})

test('renderChatMarkdown does not flash a setext heading when a nested bullet dash streams in', () => {
  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
    streaming: true,
  }
  // A must stay a plain bold item through the whole lead-up to B: the bare dash
  // must not flash <h2>, and the next bullet's `*`/`**` (before its text) must
  // not flash an empty nested <li> under A.
  for (const partial of ['1. **AAAAA**\n   - ', '1. **AAAAA**\n   - *', '1. **AAAAA**\n   - **']) {
    const html = stripFadeTail(renderChatMarkdown(partial, options))
    assert.doesNotMatch(html, /<h2/)
    assert.doesNotMatch(html, /<ul>/)
    assert.match(html, /<ol>\s*<li><strong>AAAAA<\/strong><\/li>\s*<\/ol>/)
  }
})

test('renderChatMarkdown streams a bold ordered list without literal-marker flicker', () => {
  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
    streaming: true,
  }
  // Next item's marker has arrived but its content/closing ** has not.
  const html = stripFadeTail(renderChatMarkdown('1. **速游**\n2. *', options))
  assert.doesNotMatch(html, /<li>\*+<\/li>/)
  assert.match(html, /<li><strong>速游<\/strong><\/li>/)
})

test('markStandaloneStrongParagraphs tags only paragraphs that are entirely one bold run', () => {
  // A model emitting **小节标题** as a pseudo-heading: should be tagged.
  assert.equal(
    markStandaloneStrongParagraphs('<p><strong>小节标题：</strong></p>'),
    '<p class="md-strong-title"><strong>小节标题：</strong></p>',
  )

  // Mid-stream body paragraph with one completed bold run plus body text must
  // NOT be tagged (this is the streaming spacing-jump regression).
  for (const html of [
    '<p>正文 <strong>加粗部分</strong> 与 **</p>',
    '<p>正文 <strong>A</strong> 与 <strong>B</strong> 收尾</p>',
    '<p><strong>加粗</strong> 后续文字</p>',
  ]) {
    assert.equal(markStandaloneStrongParagraphs(html), html)
  }

  // A bold list item (loose list wraps content in <p>) is list text, not a
  // subtitle: it must keep the list's own margins, not gain a 1.25em top margin.
  assert.equal(
    markStandaloneStrongParagraphs('<ol><li><p><strong>AAA</strong></p></li></ol>'),
    '<ol><li><p><strong>AAA</strong></p></li></ol>',
  )
})

test('renderChatMarkdown does not give a text-heavy paragraph the strong-title margin mid-stream', () => {
  const renderer = createChatMarkdownRenderer()
  const options = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
    streaming: true,
  }
  const head = '#### **总结与建议**\n\n'
  // One bold run closed inside an otherwise text-heavy body paragraph.
  const midStream = head + '总体来看，体现在 **"补短板"（高端核心部件）** 与 **'
  const html = renderChatMarkdown(midStream, options)
  assert.doesNotMatch(html, /<p class="md-strong-title">总体来看/)
  assert.match(html, /<h4><strong>总结与建议<\/strong><\/h4>/)
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

test('joinCitationTagsToPreviousLine does not merge citations onto an unlabeled closing fence on a single newline', () => {
  const tag = '<kb doc="guide.pdf" chunk_id="1" />'
  const input = '```\nAPR = principal\n```\n' + tag
  assert.equal(joinCitationTagsToPreviousLine(input), '```\nAPR = principal\n```\n' + tag)
})

test('applyStreamingTailFade wraps the trailing text run', () => {
  const out = applyStreamingTailFade('<p>Great, that narrows it down a lot. Two more quick</p>')
  assert.match(out, /<span class="stream-fade-tail">[^<]*Two more quick<\/span><\/p>$/)
})

test('applyStreamingTailFade skips whitespace-only runs and fades the last list item', () => {
  const out = applyStreamingTailFade('<ol>\n<li>第一项</li>\n<li>正在生成的第二项</li>\n</ol>')
  assert.match(out, /<li><span class="stream-fade-tail">正在生成的第二项<\/span><\/li>/)
})

test('applyStreamingTailFade is a no-op for empty content', () => {
  assert.equal(applyStreamingTailFade(''), '')
  assert.equal(applyStreamingTailFade('<p></p>'), '<p></p>')
})

test('renderChatMarkdown adds the tail fade only while streaming', () => {
  const renderer = createChatMarkdownRenderer()
  const opts = {
    renderer,
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
  }
  const streamed = renderChatMarkdown('正在生成中的回答内容', { ...opts, streaming: true })
  assert.match(streamed, /stream-fade-tail/)
  const settled = renderChatMarkdown('正在生成中的回答内容', { ...opts, streaming: false })
  assert.doesNotMatch(settled, /stream-fade-tail/)
})

test('renderChatMarkdown keeps an unlabeled fenced code block closed when a citation immediately follows', () => {
  const renderer = createChatMarkdownRenderer()
  const tag = '<kb doc="guide.pdf" chunk_id="1" />'
  const html = renderChatMarkdown(
    ['```', 'APR = principal', '```', tag, '', '### 重要性'].join('\n'),
    {
      renderer,
      escapeMarkdown: (text) => text,
      sanitizeHtml: (html) => html,
    },
  )

  assert.doesNotMatch(html, /### 重要性/)
  assert.match(html, /<h3>重要性<\/h3>/)
  assert.equal((html.match(/<pre>/g) || []).length, 1)
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
