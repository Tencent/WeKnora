import assert from 'node:assert/strict'
import test from 'node:test'

import { marked } from 'marked'

import { renderChatMarkdown } from './chatMarkdownRenderer.ts'
import { parseWeKnoraEntityURI, renderWeKnoraEntityLinks } from './weknoraEntityLinks.ts'

const LABELS = { kb: '知识库', doc: '文档', chunk: '片段' }
const KB_ID = 'kb-0001'
const DOC_ID = '00000001-0000-4000-8000-000000000001'

test('parseWeKnoraEntityURI accepts only whole durable URIs', () => {
  assert.deepEqual(parseWeKnoraEntityURI(`weknora://kb/${KB_ID}`), { kind: 'kb', id: KB_ID })
  assert.deepEqual(parseWeKnoraEntityURI(`WeKnora://DOC/${DOC_ID}`), { kind: 'doc', id: DOC_ID })
  assert.equal(parseWeKnoraEntityURI('weknora://kb/'), null)
  assert.equal(parseWeKnoraEntityURI('weknora://user/1'), null)
  assert.equal(parseWeKnoraEntityURI(`see weknora://kb/${KB_ID}`), null)
  assert.equal(parseWeKnoraEntityURI('javascript:alert(1)'), null)
})

test('markdown links to weknora URIs keep their text and become entity links', () => {
  const html = renderWeKnoraEntityLinks(
    `<p>存入 <a href="weknora://kb/${KB_ID}">每日热榜</a> 即可</p>`,
    LABELS,
  )
  assert.equal(
    html,
    '<p>存入 <span class="weknora-entity-link" role="link" tabindex="0" title="知识库" ' +
      `data-entity-kind="kb" data-entity-id="${KB_ID}">每日热榜</span> 即可</p>`,
  )
})

test('bare weknora URIs in prose show the real ID with the kind as tooltip', () => {
  const html = renderWeKnoraEntityLinks(`<p>参考 weknora://doc/${DOC_ID}。</p>`, LABELS)
  assert.match(html, new RegExp(`title="文档" data-entity-kind="doc" data-entity-id="${DOC_ID}">${DOC_ID}</span>。`))
  assert.doesNotMatch(html, /weknora:\/\//)
})

test('weknora URIs inside code, pre and ordinary links are left alone', () => {
  const input =
    `<p><code>weknora://kb/${KB_ID}</code></p>` +
    `<pre><code>weknora://kb/${KB_ID}</code></pre>` +
    `<p><a href="https://example.com">weknora://kb/${KB_ID}</a></p>`
  assert.equal(renderWeKnoraEntityLinks(input, LABELS), input)
})

test('renderChatMarkdown renders weknora links only when labels are supplied', () => {
  const markdown = `把结果写入 [热榜知识库](weknora://kb/${KB_ID})，并参考 weknora://doc/${DOC_ID}`
  const options = {
    renderer: new marked.Renderer(),
    escapeMarkdown: (text: string) => text,
    sanitizeHtml: (html: string) => html,
  }
  const plain = renderChatMarkdown(markdown, options)
  assert.doesNotMatch(plain, /weknora-entity-link/)

  const linked = renderChatMarkdown(markdown, { ...options, entityLinkLabels: LABELS })
  assert.match(linked, new RegExp(`data-entity-kind="kb" data-entity-id="${KB_ID}">热榜知识库</span>`))
  assert.match(linked, new RegExp(`data-entity-kind="doc" data-entity-id="${DOC_ID}">${DOC_ID}</span>`))
  assert.doesNotMatch(linked, /href="weknora:/)
})
