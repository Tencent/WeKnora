import assert from 'node:assert/strict'
import test from 'node:test'

import { marked } from 'marked'

import { createWikiMarkdownRenderer } from './wikiMermaid.ts'

const parse = (md: string) =>
  marked.parse(md, { breaks: true, async: false, renderer: createWikiMarkdownRenderer() }) as string

test('mermaid fences become pending diagram blocks', () => {
  const html = parse('```mermaid\ngraph TD\n  A --> B & C\n```\n')
  assert.match(html, /<pre class="mermaid" data-mermaid="false"><code>/)
  assert.ok(html.includes('A --&gt; B &amp; C'))
})

test('other code fences keep the default rendering', () => {
  const html = parse('```js\nconst a = 1\n```\n')
  assert.match(html, /<pre><code class="language-js">const a = 1\n<\/code><\/pre>/)
  assert.ok(!html.includes('data-mermaid'))
})
