import test from 'node:test'
import assert from 'node:assert/strict'
import { resolvePdfSource, mergeSourceRects } from './pdfSourceLocate.ts'
import { findInText, findTextMatches, normalizeForMatch, selectQuoteForSentence, parseSourceLocators, sourceQuoteText } from './sourceLocator.ts'

const resolve = (pages: string[], request: Partial<Parameters<typeof resolvePdfSource>[0]>) =>
  resolvePdfSource({ locators: [], quotes: [], token: 1, ...request }, pages.length, async (p) => pages[p - 1]!)

test('decimal, negative, percentage and comparison differences never match', () => {
  for (const [source, quote] of [
    ['限值为15毫米', '限值为1.5毫米'], ['温度为5度', '温度为-5度'],
    ['增长为5', '增长为5%'], ['值小于<5', '值小于>5'], ['时间12:30', '时间1230'],
    ['比例为1/2', '比例为12'], ['阈值≤5', '阈值≥5'],
  ]) assert.equal(findInText(source!, quote!), null, `${source} / ${quote}`)
  assert.ok(findInText('限值为１.５毫米', '限值为1.5毫米'))
})

test('partial common opening does not manufacture a full quote', () => {
  const common = '设备进入施工现场以后必须按照规定检查所有安全措施'
  assert.equal(findInText(common + '本次验收没有通过', common + '本次验收已经通过'), null)
})

test('duplicates are ambiguous and supplementary characters have correct UTF-16 ranges', () => {
  assert.equal(findInText('重复原文。重复原文。', '重复原文'), null)
  assert.equal(findTextMatches('重复原文。重复原文。', '重复原文').length, 2)
  const text = '前文𠀀原文片段𠀁后文'
  const hit = findInText(text, '𠀀原文片段𠀁')!
  assert.equal(text.slice(hit.start, hit.end), '𠀀原文片段𠀁')
})

test('a paraphrase retains full source context rather than an unrelated opening', () => {
  const source = '第一部分讨论背景。第二部分试验失败。'
  assert.deepEqual(selectQuoteForSentence(source, 'The experiment failed.'), [source])
})

test('malformed pages and boxes cannot be drawn', () => {
  assert.deepEqual(parseSourceLocators([{ type: 'pdf', page: 0 }, { type: 'pdf', page: 1.5 }]), [])
  for (const bbox of [[NaN, 0, 1, 1], [0, 0, Infinity, 1], [0, 0, 2, 1], [0.8, 0, 0.2, 1]]) {
    assert.equal(parseSourceLocators([{ type: 'pdf', page: 1, bbox }])[0]?.bbox, undefined)
  }
})

test('all pages are considered, including page 501', async () => {
  const pages = Array.from({ length: 501 }, (_, i) => `Page ${i + 1} unrelated.`)
  pages[500] = 'The uniquely cited passage.'
  const result = await resolve(pages, { scope: pages[500] })
  assert.equal(result.targets[0]?.page, 501)
})

test('duplicate passages on different pages are not silently selected', async () => {
  const result = await resolve(['设备验收合格后方可使用。', '设备验收合格后方可使用。'], { scope: '设备验收合格后方可使用。' })
  assert.deepEqual(result, { targets: [], reason: 'ambiguous' })
})

test('full chunk context disambiguates a repeated sentence and retains occurrence', async () => {
  const result = await resolve(['第一章。设备验收合格后方可使用。第二章。设备验收合格后方可使用。'], {
    scope: '第二章。设备验收合格后方可使用。', sentence: '设备验收合格后方可使用。',
  })
  assert.equal(result.targets[0]?.page, 1)
  assert.equal(result.targets[0]?.occurrence, 1)
})

test('legacy wrong-page bbox is revalidated against original text', async () => {
  const result = await resolve(['第一段正确文本。', '第二段实际内容。'], {
    scope: '第二段实际内容。', locators: [{ type: 'pdf', page: 1, bbox: [0, 0, 1, 1], quote: '第二段实际内容。' }],
  })
  assert.equal(result.targets[0]?.page, 2)
  assert.equal(result.targets[0]?.bbox, undefined)
})

test('legacy matching page does not override ambiguity elsewhere', async () => {
  const result = await resolve(['相同的原文段落。', '相同的原文段落。'], {
    locators: [{ type: 'pdf', page: 1, quote: '相同的原文段落。' }], scope: '相同的原文段落。',
  })
  assert.equal(result.reason, 'ambiguous')
})

test('mixed bbox and page targets are both resolved', async () => {
  const result = await resolve(['', '第二页的引用文本。'], { locators: [
    { type: 'pdf', page: 1, mapping: 'exact', bbox: [0.1, 0.2, 0.9, 0.4], quote: '扫描页的原文' },
    { type: 'pdf', page: 2, mapping: 'exact', quote: '第二页的引用文本。' },
  ] })
  assert.deepEqual(result.targets.map((t) => [t.page, t.granularity]), [[1, 'block'], [2, 'text']])
})

test('scanned page without text retains verified block geometry', async () => {
  const result = await resolve([''], { locators: [{ type: 'pdf', page: 1, mapping: 'exact', bbox: [0.1, 0.2, 0.8, 0.5], quote: 'OCR 文本' }] })
  assert.equal(result.targets[0]?.granularity, 'block')
})

test('cross-page quote produces separate source fragments', async () => {
  const result = await resolve(['The first part of', 'the cited passage ends here.'], { scope: 'The first part of the cited passage ends here.' })
  assert.deepEqual(result.targets.map((t) => t.page), [1, 2])
  assert.equal(normalizeForMatch(result.targets.map((t) => t.quote).join('')), normalizeForMatch('The first part of the cited passage ends here.'))
})

test('missing full chunk cannot fall back to a coincidental opening', async () => {
  const result = await resolve(['Shared opening. Completely different conclusion.'], { scope: 'Shared opening. Unique result absent.', quotes: ['Shared opening.'] })
  assert.deepEqual(result, { targets: [], reason: 'missing' })
})

test('unavailable revision does not read or highlight a replacement document', async () => {
  const result = await resolvePdfSource({ token: 1, quotes: ['old quote'], locators: [], unavailable: true }, 2, async () => { throw new Error('must not read') })
  assert.equal(result.reason, 'unavailable')
})

test('rapid citation switch cancels before consuming a stale page result', async () => {
  let cancelled = false
  const result = await resolvePdfSource({ token: 1, quotes: [], locators: [], scope: 'old citation' }, 20, async () => { cancelled = true; return 'old citation' }, () => cancelled)
  assert.deepEqual(result, { targets: [], reason: 'cancelled' })
})

test('same-height columns remain distinct while adjacent glyph runs merge', () => {
  const rects = mergeSourceRects([
    { left: 10, top: 10, width: 30, height: 12 }, { left: 42, top: 10, width: 30, height: 12 },
    { left: 250, top: 10, width: 80, height: 12 },
  ])
  assert.equal(rects.length, 2)
  assert.equal(rects[0]?.width, 62)
})

test('native DOMRect prototype getters survive rectangle merging', () => {
  // Browser DOMRect coordinates are getters on the prototype, not own fields.
  const nativeLike = Object.create({ left: 20, top: 30, width: 40, height: 12 })
  assert.deepEqual(mergeSourceRects([nativeLike]), [{ left: 20, top: 30, width: 40, height: 12 }])
})

test('a number cannot match part of a different numeric value', () => {
  for (const [source, quote] of [['增长为5%', '增长为5'], ['115', '15'], ['1.5', '15'], ['15.5', '15'], ['-15', '15']]) {
    assert.equal(findInText(source!, quote!), null, `${source} / ${quote}`)
  }
})

test('partially mapped chunks cannot claim complete precise evidence', async () => {
  const result = await resolve(['Known paragraph.', 'Actual important conclusion.'], {
    scope: 'Known paragraph. Missing important conclusion.',
    locators: [{ type: 'pdf', page: 1, mapping: 'exact', partial: true, quote: 'Known paragraph.' }],
  })
  assert.equal(result.reason, 'missing')
})


test('HTML entities preserve comparisons and are decoded only once', () => {
  assert.equal(sourceQuoteText('<td>限值&#x3c;1.5</td>'), '限值<1.5')
  assert.equal(sourceQuoteText('&amp;lt;'), '&lt;')
  assert.equal(findInText('temperature 5 C', 'temperature - 5 C'), null)
  assert.ok(findInText('temperature - 5 C', 'temperature -5 C'))
})
