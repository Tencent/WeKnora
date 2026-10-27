import assert from 'node:assert/strict'
import test from 'node:test'

import {
  normalizeHeadingText,
  stripDuplicateLeadingH1,
} from './markdownTitleDedupe.ts'
import { renderDocumentPreviewMarkdown } from './documentPreviewMarkdown.ts'

const TITLE = '线上天诛2024新版本使用培训及使用细节事项注意点'

test('strips the leading H1 when it repeats the document title', () => {
  const source = `# ${TITLE}

## 项目任务管理

- 项目协同（团队版中）
`
  const result = stripDuplicateLeadingH1(source, [TITLE])

  assert.equal(result, `## 项目任务管理

- 项目协同（团队版中）
`)
})

test('keeps the leading H1 when it differs from the document title', () => {
  const source = `# 完全不同的标题

正文段落。
`
  assert.equal(stripDuplicateLeadingH1(source, [TITLE]), source)
})

test('leaves Markdown without an H1 untouched', () => {
  const source = `## 项目任务管理

- 项目协同（团队版中）
`
  assert.equal(stripDuplicateLeadingH1(source, [TITLE]), source)
})

test('only strips the first heading, never a later H1', () => {
  const source = `## 项目任务管理

# ${TITLE}

- 项目协同（团队版中）
`
  assert.equal(stripDuplicateLeadingH1(source, [TITLE]), source)
})

test('does not strip an H2 that happens to equal the title', () => {
  const source = `## ${TITLE}

正文。
`
  assert.equal(stripDuplicateLeadingH1(source, [TITLE]), source)
})

test('matches file names with an extension and decorative title prefixes', () => {
  assert.equal(
    stripDuplicateLeadingH1(`# ${TITLE}\n\n正文。\n`, [`${TITLE}.md`]),
    '正文。\n',
  )
  assert.equal(
    stripDuplicateLeadingH1(`# ${TITLE}\n\n正文。\n`, [`【使用必看】${TITLE}`]),
    '正文。\n',
  )
  // 全角数字 / 多余空白也算同一个标题
  assert.equal(
    stripDuplicateLeadingH1('# 线上天诛２０２４新版本使用培训及使用细节事项注意点\n\n正文。\n', [TITLE]),
    '正文。\n',
  )
})

test('keeps YAML front matter while dropping the duplicated heading', () => {
  const source = `---
title: ${TITLE}
tags: [kb]
---

# ${TITLE}

正文。
`
  assert.equal(
    stripDuplicateLeadingH1(source, [TITLE]),
    `---
title: ${TITLE}
tags: [kb]
---
正文。
`,
  )
})

test('is a no-op when no usable title is supplied', () => {
  const source = `# ${TITLE}\n\n正文。\n`
  assert.equal(stripDuplicateLeadingH1(source, []), source)
  assert.equal(stripDuplicateLeadingH1(source, [null, undefined, '  ']), source)
})

test('normalization collapses display-only differences but not real text', () => {
  assert.equal(normalizeHeadingText('  【使用必看】 **天诛** 2024  '), '天诛 2024')
  assert.equal(normalizeHeadingText('`Code` [链接](https://x.test) 标题'), 'code 链接 标题')
  assert.notEqual(normalizeHeadingText('天诛 2024'), normalizeHeadingText('天诛 2025'))
})

test('document preview renders the body without the duplicated H1 but keeps rich blocks', () => {
  const source = `# ${TITLE}

| 能力 | 说明 |
| --- | --- |
| 列表 | 支持 |

\`\`\`ts
const a = 1
\`\`\`

行内公式 $E = mc^2$

- 项目协同
`
  const html = renderDocumentPreviewMarkdown(source, value => value, {
    titles: [`${TITLE}.md`],
  })

  assert.doesNotMatch(html, /<h1>/)
  assert.match(html, /<table>/)
  assert.match(html, /<pre><code class="hljs">/)
  assert.match(html, /class="katex"/)
  assert.match(html, /<ul>/)
  assert.match(html, /<li>项目协同<\/li>/)
})

test('document preview keeps an unrelated H1 intact', () => {
  const html = renderDocumentPreviewMarkdown('# 另一个标题\n\n正文。\n', value => value, {
    titles: [TITLE],
  })

  assert.match(html, /<h1>另一个标题<\/h1>/)
})
