import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'

const source = readFileSync(new URL('./ToolApprovalCard.vue', import.meta.url), 'utf8')
const styles = readFileSync(new URL('./agent-interaction-card.less', import.meta.url), 'utf8')
const descriptionCode = source.slice(
  source.indexOf('const descriptionParts ='), source.indexOf('const isJsonValid ='),
).replace(' as string[]', '')
const parse = description => JSON.parse(JSON.stringify(runInNewContext(
  descriptionCode + '\ndescriptionParts', { props: { description }, computed: fn => fn() },
)))

test('batch approval retains every title in the separate item list', () => {
  const titles = Array.from({ length: 20 }, (_, i) => `Document ${i + 1}`)
  const description = ['Delete documents', 'Finance', ...titles.map(title => '- ' + title)].join('\n')
  assert.deepEqual(parse(description), { head: 'Delete documents\nFinance', fields: [], items: titles })
})

test('legacy multiline titles remain readable through the scrollable fallback', () => {
  const description = 'Delete documents\nFinance\n- First\ncontinued title\n- Last document'
  assert.deepEqual(parse(description), { head: description, fields: [], items: [] })
  assert.match(source, /class="prompt-desc">\{\{ descriptionParts\.head \}\}/)
  const fallback = styles.match(/\.prompt-desc \{([\s\S]*?)\n\}/)?.[1]
  assert.ok(fallback)
  assert.match(fallback, /white-space:\s*pre-line/)
  assert.match(fallback, /overflow-y:\s*auto/)
  assert.doesNotMatch(fallback, /line-clamp|overflow:\s*hidden/)
})

test('setting lines render as labeled fields and the title stays the headline', () => {
  const description = [
    '创建知识库',
    '助手测试',
    'Type: document',
    'Embedding model: builtin-embedding',
    'Summary model: Qwen',
    'Indexes: wiki on',
  ].join('\n')
  assert.deepEqual(parse(description), {
    head: '创建知识库\n助手测试',
    fields: [
      { label: 'Type', value: 'document' },
      { label: 'Embedding model', value: 'builtin-embedding' },
      { label: 'Summary model', value: 'Qwen' },
      { label: 'Indexes', value: 'wiki on' },
    ],
    items: [],
  })
  assert.match(source, /class="prompt-fields"/)
  assert.match(styles, /\.prompt-fields \{/)
})
