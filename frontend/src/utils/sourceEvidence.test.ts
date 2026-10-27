import { test } from 'node:test'
import assert from 'node:assert/strict'
import { quotedSourceExcerpt, selectLocatorsForSentence, selectQuoteForSentence } from './sourceLocator.ts'
import { sourceImageDigest } from './sourceImage.ts'
test('a paraphrase with a verbatim quotation selects only supporting evidence', () => {
 const quote = '小家团圆美满，大国繁荣昌盛'
 const sentence = `物业在双节期间向业主致以节日祝福，倡导“${quote}”的家国情怀`
 const source = `开头问候。\n\n愿家国同辉：${quote}，双喜临门。`
 assert.equal(quotedSourceExcerpt(source, sentence), quote)
 assert.deepEqual(selectQuoteForSentence(source, sentence), [quote])
 const locs = [{ type: 'docx', quote: '开头问候。' }, { type: 'docx', quote: source.split('\n\n')[1] }]
 assert.deepEqual(selectLocatorsForSentence(locs, sentence), [locs[1]])
 assert.equal(quotedSourceExcerpt('其他内容。', sentence), undefined)
 assert.equal(quotedSourceExcerpt(`${source}\n${source}`, sentence), undefined)
})
test('quotation does not relax numeric matching', () => {
 assert.equal(quotedSourceExcerpt('The cited limit is 1.5 millimeters.', 'Claim: "The cited limit is 15 millimeters."'), undefined)
})
test('source image SHA256 identifies exact bytes', async () => {
 assert.equal(await sourceImageDigest(new TextEncoder().encode('abc').buffer), 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
 assert.notEqual(await sourceImageDigest(new TextEncoder().encode('abd').buffer), await sourceImageDigest(new TextEncoder().encode('abc').buffer))
})
