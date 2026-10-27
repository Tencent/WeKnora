import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'

const source = readFileSync(new URL('./ToolApprovalCard.vue', import.meta.url), 'utf8')
const styles = readFileSync(new URL('./agent-interaction-card.less', import.meta.url), 'utf8')
const descriptionCode = source.slice(
  source.indexOf('const descriptionParts ='), source.indexOf('const isJsonValid ='),
).replace(/^interface .*$/m, '').replace(/ as \w+\[\]/g, '')
const evaluate = (description, serviceName = 'docs-mcp', delegatedTask = '') => runInNewContext(
  descriptionCode + '\n;({ descriptionParts, question, compactDescription, hasLongDescription, argumentSummary })',
  {
    props: { description, serviceName, delegatedTask },
    argsText: { value: '{"period":2,"limit":3}' },
    computed: fn => ({ value: fn() }),
    isOperation: { value: serviceName === 'WeKnora' },
    targetLabel: { value: serviceName + ' › tool' },
    t: (key, params) => key.split('.').pop() + ' ' + JSON.stringify(params),
  },
)
const parse = (description, serviceName) =>
  JSON.parse(JSON.stringify(evaluate(description, serviceName).descriptionParts.value))

test('batch approval retains every title in the separate item list', () => {
  const titles = Array.from({ length: 20 }, (_, i) => `Document ${i + 1}`)
  const description = ['Delete documents', 'Finance', ...titles.map(title => '- ' + title)].join('\n')
  assert.deepEqual(parse(description), { head: 'Delete documents\nFinance', fields: [], items: titles, action: '', target: '' })
})

test('legacy multiline titles remain readable through the scrollable fallback', () => {
  const description = 'Delete documents\nFinance\n- First\ncontinued title\n- Last document'
  assert.deepEqual(parse(description), { head: description, fields: [], items: [], action: '', target: '' })
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
    action: '',
    target: '',
  })
  assert.match(source, /class="prompt-fields"/)
  assert.match(styles, /\.prompt-fields \{/)
})

test('a WeKnora operation asks with its summary and target, and counts move out of labels', () => {
  const description = [
    '创建MCP服务',
    '汇率查询 MCP',
    'This replaces the stored credentials.',
    'URL: https://example.com/mcp',
    'Usage instructions (92 characters): 查询汇率',
  ].join('\n')
  const { descriptionParts, question } = evaluate(description, 'WeKnora')
  assert.deepEqual(JSON.parse(JSON.stringify(descriptionParts.value)), {
    head: 'This replaces the stored credentials.',
    fields: [
      { label: 'URL', value: 'https://example.com/mcp' },
      { label: 'Usage instructions', value: '查询汇率', count: '92' },
    ],
    items: [],
    action: '创建MCP服务',
    target: '汇率查询 MCP',
  })
  assert.equal(question.value, 'operationQuestion {"action":"创建MCP服务","target":"汇率查询 MCP"}')
  assert.match(styles, /\.prompt-field \{\s*display: contents/)
})

test('a warning right after the summary is not mistaken for the target', () => {
  const { descriptionParts, question } = evaluate('Update MCP service\nThis drops existing tools.', 'WeKnora')
  assert.equal(descriptionParts.value.target, '')
  assert.equal(descriptionParts.value.head, 'This drops existing tools.')
  assert.equal(question.value, 'operationQuestionNoTarget {"action":"Update MCP service"}')
})

test('delegated approval keeps task separate from description and shows actual arguments', () => {
  const result = evaluate('hotlist: Read article rankings.\n\n## Parameters\n- period: days\n\n## Examples\n{}', 'KM', 'hotlist')
  assert.equal(result.compactDescription.value, 'Read article rankings.')
  assert.equal(result.hasLongDescription.value, true)
  assert.match(result.argumentSummary.value, /period=2.*limit=3/)
  assert.doesNotMatch(result.descriptionParts.value.head, /^hotlist:/)
})
