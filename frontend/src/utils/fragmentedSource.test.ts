import test from 'node:test'
import assert from 'node:assert/strict'
import { findSourceFragments } from './fragmentedSource'
import { resolvePdfSource } from './pdfSourceLocate'
import { sourceImageContext, sourceQuoteText } from './sourceLocator'
import { mergeDocumentReferences, resolveReferenceSource } from './referenceSources'
import { preprocessCitationTags } from './citationMarkdown'

test('Agent grouping retains every cited chunk identity and resolves the exact document', () => {
  const refs = mergeDocumentReferences([
    { id: 'first', knowledge_id: 'doc', knowledge_title: '微信&企微.docx', content: 'first evidence' },
    { id: 'second', knowledge_id: 'doc', knowledge_title: '微信&企微.docx', content: 'second evidence' },
  ])
  assert.deepEqual(refs[0]?.chunk_ids, ['first', 'second'])
  const source = resolveReferenceSource(refs, { chunkId: 'second', openSource: true })
  assert.equal(source?.knowledgeId, 'doc')
  assert.equal(source?.chunkId, 'second')
  assert.equal(source?.content, undefined, 'grouped content must be fetched for the cited chunk')
})

test('encoded citation titles are decoded once before HTML escaping', () => {
  const html = preprocessCitationTags('<kb doc="微信&amp;企微.docx" id="kb" chunk_id="chunk"/>')
  assert.ok(!html.includes('&amp;amp;'))
  assert.ok(html.includes('微信&amp;企微.docx'))
})

test('column-serialized table identifies a row-serialized page by independent cells', async () => {
  const pages = ['机器人研究社（周四） 张老师 教室A\n天文观测社（周四） 李老师 教室B', '机器人研究社（周四） 张老师 操场']
  const scope = '|课程|老师|地点|\n|机器人研究社（周四） 天文观测社（周四）|张老师 李老师|教室A 教室B|'
  const result = await resolvePdfSource({ token: 1, quotes: [], locators: [], scope, sentence: '天文观测社每周四开课。' }, pages.length, async p => pages[p - 1]!)
  assert.equal(result.reason, 'partial')
  assert.equal(result.targets[0]?.page, 1)
  assert.ok(result.targets[0]?.quote?.includes('天文观测社'))
  assert.ok(result.targets.every(t => t.page === 1))
})

test('a conjunction of repeated cells must identify a unique page', () => {
  const scope = '|机器人研究社（周四） 天文观测社（周四）|'
  const pages = ['机器人研究社（周四） 张老师 天文观测社（周四） 李老师', '机器人研究社（周四）', '天文观测社（周四）']
  assert.equal(findSourceFragments(pages, scope).length, 2)
  assert.deepEqual(findSourceFragments([pages[0]!, pages[0]!], scope), [])
})

test('fragment recovery cannot claim a missing numeric value or a coincidental opening', () => {
  assert.deepEqual(findSourceFragments(['Shared opening. Different conclusion.'], 'Shared opening. Missing conclusion.'), [])
  assert.deepEqual(findSourceFragments(['甲项目温度15度\n乙项目限值20毫米'], '|甲项目温度1.5度|乙项目限值2.0毫米|'), [])
})

test('parser artifacts retain only verified sentences and explicitly report partial coverage', async () => {
  const scope = '绑定手机：点击账号à选择个人中心。\n\n学生选课开始时间为周一上午十一点。小学部高年级选课在周一下午一点开始。'
  const pages = ['绑定手机：点击账号→选择个人中心。学生选课开始时间为周一上午十一点。小学部高年级选课在周一下午一点开始。']
  const result = await resolvePdfSource({ token: 1, quotes: [], locators: [], scope }, 1, async () => pages[0]!)
  assert.equal(result.reason, 'partial')
  assert.equal(result.targets.length, 3)
  assert.ok(result.targets.every(t => !t.quote?.includes('à')))
  assert.ok(result.targets.some(t => t.quote === '绑定手机：点击账号'))
})

test('OCR image context requires exact resource identity and at least one neighboring paragraph', () => {
  const md = '前一段明确来源。\n\n![descript](resource://image)\n\n后一段明确来源。'
  assert.deepEqual(sourceImageContext(md, ['resource://image']), { before: '前一段明确来源。', after: '后一段明确来源。' })
  assert.equal(sourceImageContext(md, ['resource://another-image']), undefined)
  assert.deepEqual(sourceImageContext(md.replace('后一段明确来源。', ''), ['resource://image']), { before: '前一段明确来源。', after: '' })
  assert.equal(sourceImageContext(md + '\n\n' + md, ['resource://image']), undefined)
  assert.equal(sourceQuoteText(md).includes('descript'), false)
})

test('title fallback rejects duplicate document titles even within one knowledge base', () => {
  const refs = ['doc-one', 'doc-two'].map(knowledge_id => ({ id: knowledge_id, knowledge_id, knowledge_title: 'Same.docx', knowledge_base_id: 'kb' }))
  assert.equal(resolveReferenceSource(refs, { chunkId: 'missing-chunk', documentTitle: 'Same.docx', knowledgeBaseId: 'kb' }), null)
})

test('decoding a citation attribute never turns encoded markup into active HTML', () => {
  const html = preprocessCitationTags('<kb doc="&lt;img src=x onerror=alert(1)&gt;.docx" chunk_id="chunk"/>')
  assert.ok(html.includes('data-doc="&lt;img'))
  assert.ok(!html.includes('<img'))
})
