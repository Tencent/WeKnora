import assert from 'node:assert/strict'
import test from 'node:test'

import {
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
  )
  assert.match(injected, /id="m-1" data-mermaid="cached"><svg id="first">/)
  assert.match(injected, /id="m-2" data-mermaid="cached"><svg id="second">/)
  assert.equal(injected.includes('graph TD'), false)
})

test('prepareStreamingMermaidMarkdown masks a trailing incomplete mermaid fence', () => {
  const prepared = prepareStreamingMermaidMarkdown('Hello\n```mermaid\ngraph TD\n  A', LOADING)
  assert.equal(prepared.includes('```mermaid'), false)
  assert.match(prepared, /chat-mermaid-block--loading/)
  assert.match(prepared, /Hello/)
})
