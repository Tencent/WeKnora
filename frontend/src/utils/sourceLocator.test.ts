import test from 'node:test'
import assert from 'node:assert/strict'

import {
  findInText,
  findNormalized,
  locatorPages,
  normalizeForMatch,
  parseSourceLocators,
  selectLocatorsForSentence,
  selectQuoteForSentence,
  textFragmentUrl,
} from './sourceLocator.ts'
import { anchorWithContext, lastSentence } from './citationAnchor.ts'
import { mergeDocumentReferences, resolveReferenceSource } from './referenceSources.ts'
import { resolveEpubPath } from './epubPreview.ts'
import { resolvePreviewKind } from './filePreview.ts'

test('normalization preserves numeric symbols while folding width and case', () => {
  assert.equal(normalizeForMatch('８.１４ 索夹，Ｈello!'), '8.14索夹hello')
})

test('findInText maps a normalized match back to the raw text', () => {
  const text = '8.14.11 索夹与吊索施工应符合下列规定：\n1 在满足施工需要的前提下，应减小猫道面层开孔面积。'
  const hit = findInText(text, '在满足施工需要的前提下,应减小猫道面层开孔面积')
  assert.ok(hit)
  assert.equal(text.slice(hit.start, hit.end), '在满足施工需要的前提下，应减小猫道面层开孔面积')
})

test('findNormalized tolerates only an omitted leading list label', () => {
  const hay = normalizeForMatch('前文。紧固同一索夹的螺栓时，各螺栓受力应均匀，并应按规定顺序施拧。后文')
  const m = findNormalized(hay, '（二）紧固同一索夹的螺栓时，各螺栓受力应均匀，并应按规定顺序施拧')
  assert.ok(m)
  assert.ok(m.score >= 0.5)
  assert.equal(findNormalized(hay, '完全无关的一段内容不应该被匹配到这里来吧'), null)
})



test('selectLocatorsForSentence narrows to the locator that supports the sentence', () => {
  const locs = [
    { type: 'pdf', page: 57, quote: '8.14.11 索夹与吊索施工应符合下列规定' },
    { type: 'pdf', page: 57, quote: '1 在满足施工需要的前提下，应减小猫道面层开孔面积，并应在开孔位置四周绑扎防滑木条，设立警示标志。' },
    { type: 'pdf', page: 57, quote: '2 索夹在主缆上定位后，应紧固螺栓。' },
  ]
  const picked = selectLocatorsForSentence(locs, '在满足施工需要的前提下，应减小猫道面层开孔面积，并应在开孔位置四周绑扎防滑木条，设立警示标志。')
  assert.equal(picked.length, 1)
  assert.equal(picked[0], locs[1])
  assert.equal(selectLocatorsForSentence(locs, '毫不相干的一句话').length, 3)
})

test('selectQuoteForSentence prefers the supporting sentence of the chunk', () => {
  const content = '索夹在主缆上定位后，应紧固螺栓。吊运物体时，作业人员不得沿主缆顶面行走。'
  const [first] = selectQuoteForSentence(content, '吊运物体时作业人员不得沿主缆顶面行走')
  assert.equal(first, '吊运物体时，作业人员不得沿主缆顶面行走')
  assert.equal(selectQuoteForSentence(content, '')[0], content)
})

test('parseSourceLocators and locatorPages', () => {
  const locs = parseSourceLocators([{ type: 'pdf', page: 3 }, null, { page: 2 }, { type: 'pdf', page: 3 }, { type: 'pdf', page: 1 }])
  assert.equal(locs.length, 3)
  assert.deepEqual(locatorPages(locs), [3, 1])
  assert.deepEqual(parseSourceLocators('nope'), [])
})

test('textFragmentUrl builds a scroll-to-text link', () => {
  assert.equal(
    textFragmentUrl('https://example.com/a#old', 'safety rules - part 1'),
    'https://example.com/a#:~:text=safety%20rules%20%2D%20part%201',
  )
  const long = textFragmentUrl('https://example.com/a', 'x'.repeat(50) + ' ' + 'y'.repeat(50))
  assert.match(long, /#:~:text=x+,.*y+$/)
})





test('lastSentence keeps the cited sentence and extends short ones', () => {
  assert.equal(lastSentence('第一句很长很长的内容。第二句也是完整的句子。'), '第二句也是完整的句子。')
  assert.equal(lastSentence('前一句足够长的内容。短句。'), '前一句足够长的内容。短句。')
})

test('anchorWithContext borrows the previous block for a marker on a label line', () => {
  const para = '在索夹吊装作业开始前，必须对主缆表面进行清理。'
  assert.equal(anchorWithContext('依据：', [para, '2. 清理主缆表面']), `${para}依据：`)
  assert.equal(anchorWithContext('索夹就位后应立即进行初拧。', [para]), '索夹就位后应立即进行初拧。')
  assert.equal(anchorWithContext('依据：', []), '依据：')
})

test('resolveReferenceSource picks the cited chunk of a document reference', () => {
  const refs = [
    { id: 'c1', knowledge_id: 'k1', knowledge_filename: 'a.pdf', knowledge_title: 'A', content: 'x', source_locators: [{ type: 'pdf', page: 2 }] },
    { id: 'c2', chunk_ids: ['c2', 'c3'], knowledge_id: 'k2', knowledge_title: 'B' },
    { id: 'w', chunk_type: 'web_search', knowledge_id: 'k3' },
    { id: 'f', chunk_type: 'faq', knowledge_id: 'k4' },
  ]
  const exact = resolveReferenceSource(refs, { chunkId: 'c1', anchorText: 's' })
  assert.equal(exact?.knowledgeId, 'k1')
  assert.equal(exact?.locators?.[0].page, 2)
  assert.equal(exact?.anchorText, 's')
  const grouped = resolveReferenceSource(refs, { chunkId: 'c3' })
  assert.equal(grouped?.knowledgeId, 'k2')
  assert.equal(grouped?.chunkId, 'c3')
  assert.equal(grouped?.locators, undefined, 'a grouped match belongs to another chunk')
  assert.equal(resolveReferenceSource(refs, { chunkId: 'w' }), null)
  assert.equal(resolveReferenceSource(refs, { chunkId: 'f' }), null)
  assert.equal(resolveReferenceSource(refs, { url: 'https://x' }), null)
})

test('the reference drawer checks numbers only against the exact recorded chunk', () => {
  const refs = [{
    id: 'c1',
    knowledge_id: 'k1',
    knowledge_title: 'Synthetic log policy',
    content: '操作日志保留 30 天。',
  }]
  const claimText = '操作日志保留 90 天。'
  assert.deepEqual(resolveReferenceSource(refs, { chunkId: 'c1', claimText })?.numericConflict, {
    answer: '90 天',
    source: '30 天',
  })
  assert.equal(resolveReferenceSource(refs, {
    chunkId: 'c1', anchorText: claimText,
  })?.numericConflict, undefined, 'an alignment anchor can include text outside the cited claim')
  assert.equal(resolveReferenceSource(refs, {
    chunkId: 'c1', claimText: '备份日志保留 90 天。',
  })?.numericConflict, null)
})

test('the drawer keeps navigation for merged excerpts without treating them as exact evidence', () => {
  const document = { knowledge_id: 'k1', knowledge_title: 'Synthetic log policy' }
  const refs = mergeDocumentReferences([
    { ...document, id: 'c1', content: 'The policy governs log storage.' },
    { ...document, id: 'c2', content: 'Log retention is 30 days.' },
  ])
  const claimText = 'Log retention is 90 days.'
  const target = resolveReferenceSource(refs, { chunkId: 'c1', claimText })
  assert.equal(target?.knowledgeId, 'k1')
  assert.equal(target?.content, 'The policy governs log storage.\n\nLog retention is 30 days.')
  assert.equal(target?.numericConflict, undefined)
  assert.equal(resolveReferenceSource(refs, { chunkId: 'c2', claimText })?.numericConflict, undefined)
})

test('context expanded from other chunks does not count as exact citation evidence', () => {
  const refs = [{
    id: 'c1',
    sub_chunk_id: ['c1', 'c2'],
    knowledge_id: 'k1',
    content: 'The policy governs log storage. Log retention is 30 days.',
  }]
  const target = resolveReferenceSource(refs, { chunkId: 'c1', claimText: 'Log retention is 90 days.' })
  assert.equal(target?.knowledgeId, 'k1')
  assert.equal(target?.numericConflict, undefined)
})

test('merged text stays ineligible when results repeat the same chunk id', () => {
  const base = { id: 'c1', knowledge_id: 'k1' }
  const refs = mergeDocumentReferences([
    { ...base, content: 'The policy governs log storage.' },
    { ...base, content: 'Log retention is 30 days.' },
  ])
  const target = resolveReferenceSource(refs, { chunkId: 'c1', claimText: 'Log retention is 90 days.' })
  assert.equal(target?.knowledgeId, 'k1')
  assert.equal(target?.numericConflict, undefined)
})

test('EPUB paths resolve against their document and preview as epub', () => {
  assert.equal(resolveEpubPath('OEBPS/text/ch1.xhtml', '../images/a%20b.png#x'), 'OEBPS/images/a b.png')
  assert.equal(resolveEpubPath('OEBPS/content.opf', 'ch1.xhtml'), 'OEBPS/ch1.xhtml')
  assert.equal(resolvePreviewKind('epub'), 'epub')
})


test('citation matching treats composed and decomposed accents as the same text', () => {
  assert.equal(normalizeForMatch('Café'), normalizeForMatch('Cafe\u0301'))
  assert.notEqual(normalizeForMatch('Café'), normalizeForMatch('Cafe'))
  assert.equal(normalizeForMatch('Ångström'), normalizeForMatch('A\u030Angstro\u0308m'))
  assert.equal(normalizeForMatch('각도'), normalizeForMatch('\u1100\u1161\u11A8도'))
})

test('canonical matching returns exact original UTF-16 spans including combining marks', () => {
  for (const [text, quote, expected] of [
    ['前言 Cafe\u0301 后记', 'Café', 'Cafe\u0301'],
    ['前言 Café 后记', 'Cafe\u0301', 'Café'],
    ['𝒜前言 cafe\u0301 后记', 'CAFÉ', 'cafe\u0301'],
    ['前言 A\u030Angstro\u0308m 后记', 'Ångström', 'A\u030Angstro\u0308m'],
    ['前言 \u1100\u1161\u11A8도 后记', '각도', '\u1100\u1161\u11A8도'],
  ]) {
    const hit = findInText(text, quote)
    assert.ok(hit, text)
    assert.equal(text.slice(hit.start, hit.end), expected)
  }
})
