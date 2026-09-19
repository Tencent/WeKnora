import assert from 'node:assert/strict'
import test from 'node:test'

import {
  encodeMermaidRenderError,
  extractMermaidCodes,
  injectCachedMermaidSvg,
  maskMermaidBlocksForStreaming,
  prepareStreamingMermaidMarkdown,
} from './mermaidStreaming.ts'

const LOADING = `<div class="chat-mermaid-block chat-mermaid-block--loading">
  <div class="chat-mermaid-block__header"><span class="chat-mermaid-block__badge">图表</span></div>
  <div class="streaming-mermaid-loading" aria-hidden="true"><span class="streaming-mermaid-loading__skeleton"></span></div>
</div>`

function buildBlock(innerHtml: string, preAttrs = ''): string {
  const attrs = preAttrs ? ` ${preAttrs}` : ''
  return `<div class="chat-mermaid-block">
    <pre class="chat-mermaid-block__canvas mermaid"${attrs}>${innerHtml}</pre>
  </div>`
}

function buildErrorFragment(code: string, message: string): string {
  return `<div class="chat-mermaid-block__error">${message}::${code}</div>`
}

const TWO_DIAGRAMS = `Intro

\`\`\`mermaid
graph TD
    A --> B
\`\`\`

Middle

\`\`\`mermaid
sequenceDiagram
    A->>B: hi
\`\`\`

Done.
`

test('extractMermaidCodes returns every complete mermaid fence in order', () => {
  assert.deepEqual(extractMermaidCodes(TWO_DIAGRAMS), [
    'graph TD\n    A --> B',
    'sequenceDiagram\n    A->>B: hi',
  ])
})

test('maskMermaidBlocksForStreaming hides every complete mermaid fence', () => {
  const masked = maskMermaidBlocksForStreaming(TWO_DIAGRAMS, LOADING)
  assert.equal(masked.includes('```mermaid'), false)
  assert.equal([...masked.matchAll(/chat-mermaid-block--loading/g)].length, 2)
  assert.match(masked, /Intro/)
  assert.match(masked, /Middle/)
  assert.match(masked, /Done\./)
})

test('injectCachedMermaidSvg maps cached SVGs onto loading placeholders 1:1', () => {
  const html = `
    <p>Intro</p>
    ${LOADING}
    <p>Middle</p>
    ${LOADING}
    <p>Done.</p>
  `
  const injected = injectCachedMermaidSvg(
    html,
    ['<svg id="first"></svg>', '<svg id="second"></svg>'],
    buildBlock,
    buildErrorFragment,
  )
  assert.match(injected, /data-mermaid="cached"[^>]*>\s*<svg id="first">/)
  assert.match(injected, /data-mermaid="cached"[^>]*>\s*<svg id="second">/)
  assert.equal(injected.includes('streaming-mermaid-loading'), false)
  assert.match(injected, /Intro/)
  assert.match(injected, /Middle/)
  assert.match(injected, /Done\./)
})

test('injectCachedMermaidSvg does not swallow a cached diagram when a later skeleton exists', () => {
  const html = `
    <div class="chat-mermaid-block">
      <div class="chat-mermaid-block__header"><span class="chat-mermaid-block__badge">图表</span></div>
      <pre class="chat-mermaid-block__canvas mermaid" data-mermaid="cached"><svg id="kept"></svg></pre>
    </div>
    <p>between</p>
    ${LOADING}
  `
  const injected = injectCachedMermaidSvg(
    html,
    ['<svg id="first"></svg>', '<svg id="second"></svg>'],
    buildBlock,
    buildErrorFragment,
  )
  assert.match(injected, /<svg id="kept">/)
  assert.match(injected, /between/)
  assert.match(injected, /<svg id="first">/)
  assert.equal(injected.includes('streaming-mermaid-loading'), false)
})

test('injectCachedMermaidSvg fills unrendered mermaid canvases after the stream completes', () => {
  const html = `
    <pre class="chat-mermaid-block__canvas mermaid" id="m-1" data-mermaid="false"><code>graph TD</code></pre>
    <pre class="chat-mermaid-block__canvas mermaid" id="m-2" data-mermaid="false"><code>sequenceDiagram</code></pre>
  `
  const injected = injectCachedMermaidSvg(
    html,
    ['<svg id="first"></svg>', '<svg id="second"></svg>'],
    buildBlock,
    buildErrorFragment,
  )
  assert.match(injected, /id="m-1" data-mermaid="cached"><svg id="first">/)
  assert.match(injected, /id="m-2" data-mermaid="cached"><svg id="second">/)
  assert.equal(injected.includes('graph TD'), false)
})

test('extractMermaidCodes keeps blank fences so slots stay aligned with placeholders', () => {
  const content = 'A\n\n```mermaid\n```\n\nB\n\n```mermaid\ngraph TD\n    A --> B\n```\n'
  assert.deepEqual(extractMermaidCodes(content), ['', 'graph TD\n    A --> B'])
  assert.equal([...maskMermaidBlocksForStreaming(content, LOADING).matchAll(/chat-mermaid-block--loading/g)].length, 2)
})

test('injectCachedMermaidSvg replaces a stuck loading skeleton with a visible error once a diagram permanently fails', () => {
  const html = `
    <p>Intro</p>
    ${LOADING}
    <p>Done.</p>
  `
  const injected = injectCachedMermaidSvg(
    html,
    [encodeMermaidRenderError('graph TD\n  A(broken', 'Lexical error on line 2.')],
    buildBlock,
    buildErrorFragment,
  )
  assert.equal(injected.includes('streaming-mermaid-loading'), false)
  assert.match(injected, /data-mermaid="error"/)
  assert.match(injected, /Lexical error on line 2\./)
  assert.match(injected, /graph TD/)
  assert.match(injected, /Intro/)
  assert.match(injected, /Done\./)
})

test('injectCachedMermaidSvg leaves an unresolved (empty) slot as the loading skeleton', () => {
  const html = `${LOADING}\n${LOADING}`
  const injected = injectCachedMermaidSvg(html, ['<svg id="first"></svg>', ''], buildBlock, buildErrorFragment)
  assert.match(injected, /<svg id="first">/)
  assert.equal([...injected.matchAll(/chat-mermaid-block--loading/g)].length, 1)
})

test('injectCachedMermaidSvg marks an unrendered canvas as an error after the stream completes', () => {
  const html = `
    <pre class="chat-mermaid-block__canvas mermaid" id="m-1" data-mermaid="false"><code>graph TD</code></pre>
  `
  const injected = injectCachedMermaidSvg(
    html,
    [encodeMermaidRenderError('graph TD', 'Lexical error on line 1.')],
    buildBlock,
    buildErrorFragment,
  )
  assert.match(injected, /id="m-1" data-mermaid="error"/)
  assert.match(injected, /Lexical error on line 1\./)
  assert.equal(injected.includes('<code>graph TD</code>'), false)
})

test('prepareStreamingMermaidMarkdown masks a trailing incomplete mermaid fence', () => {
  const prepared = prepareStreamingMermaidMarkdown('Hello\n```mermaid\ngraph TD\n  A', LOADING)
  assert.equal(prepared.includes('```mermaid'), false)
  assert.match(prepared, /chat-mermaid-block--loading/)
  assert.match(prepared, /Hello/)
})
