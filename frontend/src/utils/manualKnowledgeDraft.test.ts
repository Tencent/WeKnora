import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildManualDraft,
  collectManualDraftPreferredKbIds,
  deriveManualTitle,
  resolveManualDraftKnowledgeBaseId,
  resolvePreferredKnowledgeBaseId,
  type ManualDraftReference,
} from './manualKnowledgeDraft'

const labels = { emptyAnswer: '（无内容）', sourcesHeading: '参考来源' }

test('a chat answer loses its <kb/> tags and gains a source list', () => {
  const answer = [
    '## 二元技术观',
    '',
    '> **"人们陷入非好即坏的二元看法。"** <kb doc="Interview.md" chunk_id="230a27cb-45c8-4ba9-9ba1-babea70bcd95" kb_id="441ee604-a04d-4c9a-a4bd-1c4e198e6226" />',
    '',
    '他接着以 Zig 为例 <kb doc="Interview.md" chunk_id="ef18491c-34f0-4563-bb4a-062cacbdfd95" kb_id="441ee604-a04d-4c9a-a4bd-1c4e198e6226" />',
  ].join('\n')

  const draft = buildManualDraft(answer, labels)

  assert.ok(!draft.includes('<kb'), draft)
  assert.ok(!draft.includes('chunk_id'), draft)
  assert.ok(draft.includes('> **"人们陷入非好即坏的二元看法。"**'))
  assert.ok(draft.endsWith('**参考来源**\n\n- Interview.md\n'), draft)
})

test('web citations survive as real Markdown links', () => {
  const draft = buildManualDraft('见 <web url="https://ziglang.org" title="Zig" /> 官网', labels)
  assert.equal(draft, '见 [Zig](https://ziglang.org) 官网')
})

test('a tag alone on its line leaves no blank line behind', () => {
  const draft = buildManualDraft('第一段\n<kb doc="A.md" chunk_id="1" />\n第二段', labels)
  assert.equal(draft, '第一段\n第二段\n\n---\n\n**参考来源**\n\n- A.md\n')
})

test('code blocks keep their own spacing while citations are stripped', () => {
  const answer = '```js\nfn( 1 ) ;\n\n\nconst x = 2\n```\n\n说明 <kb doc="A.md" chunk_id="1" />'
  const draft = buildManualDraft(answer, labels)
  assert.ok(draft.startsWith('```js\nfn( 1 ) ;\n\n\nconst x = 2\n```'), draft)
  assert.ok(draft.includes('\n\n说明\n'), draft)
})

test('an answer without citations is passed through untouched', () => {
  assert.equal(buildManualDraft('纯文本回答', labels), '纯文本回答')
  assert.equal(buildManualDraft('   ', labels), '（无内容）')
})

test('a long question is cut on a clause boundary, with no trailing ellipsis', () => {
  const title = deriveManualTitle(
    '互联网上的人们常常陷入对技术好坏的二元看法，这种看法如何影响了技术的多样性和创新？',
    '对话摘录',
  )
  assert.equal(title, '互联网上的人们常常陷入对技术好坏的二元看法')
})

test('a short question keeps its wording, minus the question mark', () => {
  assert.equal(deriveManualTitle('Zig 为什么值得关注？', '对话摘录'), 'Zig 为什么值得关注')
  assert.equal(deriveManualTitle('  ', '对话摘录'), '对话摘录')
})

test('a long question with no boundary is cut at the limit', () => {
  const title = deriveManualTitle('长'.repeat(60), '对话摘录')
  assert.equal(title, '长'.repeat(32))
})

test('an English question is cut on a word boundary', () => {
  const title = deriveManualTitle('How does a binary view of technology hurt diversity?', '对话摘录')
  assert.equal(title, 'How does a binary view of')
})

// Soft preference for the chat "add to knowledge base" action: collect cited
// KB ids in order, then resolve against the editor's writable candidate list.

test('cited answers collect preferred knowledge-base ids in reference order', () => {
  assert.deepEqual(
    collectManualDraftPreferredKbIds([
      { knowledge_base_id: '441ee604-a04d-4c9a-a4bd-1c4e198e6226' },
    ]),
    ['441ee604-a04d-4c9a-a4bd-1c4e198e6226'],
  )
  const refs = [
    { knowledge_base_id: 'kb-first' },
    { knowledge_base_id: 'kb-second' },
  ]
  assert.deepEqual(collectManualDraftPreferredKbIds(refs), ['kb-first', 'kb-second'])
  assert.deepEqual(collectManualDraftPreferredKbIds([...refs].reverse()), [
    'kb-second',
    'kb-first',
  ])
})

test('blank references are skipped and do not mask a later usable one', () => {
  assert.deepEqual(collectManualDraftPreferredKbIds([]), [])
  assert.deepEqual(collectManualDraftPreferredKbIds(undefined), [])
  assert.deepEqual(collectManualDraftPreferredKbIds(null), [])
  assert.deepEqual(collectManualDraftPreferredKbIds([{}]), [])
  assert.deepEqual(collectManualDraftPreferredKbIds([{ knowledge_base_id: '' }]), [])
  assert.deepEqual(collectManualDraftPreferredKbIds([{ knowledge_base_id: '   ' }]), [])
  // A web-only reference carries no knowledge_base_id field.
  const webOnly: ManualDraftReference = {}
  assert.deepEqual(collectManualDraftPreferredKbIds([webOnly]), [])
  assert.deepEqual(
    collectManualDraftPreferredKbIds([
      { knowledge_base_id: '' },
      { knowledge_base_id: 'kb-real' },
    ]),
    ['kb-real'],
  )
  // Duplicates stay once, in first-seen order.
  assert.deepEqual(
    collectManualDraftPreferredKbIds([
      { knowledge_base_id: 'kb-a' },
      { knowledge_base_id: 'kb-a' },
      { knowledge_base_id: 'kb-b' },
    ]),
    ['kb-a', 'kb-b'],
  )
})

test('resolveManualDraftKnowledgeBaseId still returns the first preferred id', () => {
  assert.equal(
    resolveManualDraftKnowledgeBaseId([{ knowledge_base_id: 'kb-first' }]),
    'kb-first',
  )
  assert.equal(resolveManualDraftKnowledgeBaseId([]), null)
})

test('a rejected preference falls through to a later eligible reference', () => {
  // Viewer-only / FAQ / unknown first citation must not win over a writable later one.
  assert.equal(
    resolvePreferredKnowledgeBaseId(['viewer-only', 'writable-doc'], ['writable-doc', 'other']),
    'writable-doc',
  )
})

test('when no preferred target matches, keep the first-option fallback', () => {
  assert.equal(
    resolvePreferredKnowledgeBaseId(['viewer-only', 'faq-kb'], ['my-doc', 'shared-doc']),
    'my-doc',
  )
  assert.equal(resolvePreferredKnowledgeBaseId(['missing'], ['only']), 'only')
})

test('an empty candidate list yields an empty selection', () => {
  assert.equal(resolvePreferredKnowledgeBaseId(['kb-a'], []), '')
  assert.equal(resolvePreferredKnowledgeBaseId([], []), '')
  assert.equal(resolvePreferredKnowledgeBaseId(undefined, []), '')
  assert.equal(resolvePreferredKnowledgeBaseId(null, ['x']), 'x')
})
