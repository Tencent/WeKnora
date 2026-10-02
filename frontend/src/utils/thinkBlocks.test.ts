import assert from 'node:assert/strict'
import test from 'node:test'
import { hasThinkMarkup, parseThinkBlocks } from './thinkBlocks.ts'

test('content without any think markup passes through untouched', () => {
  const r = parseThinkBlocks('plain answer')
  assert.equal(r.think, '')
  assert.equal(r.answer, 'plain answer')
  assert.equal(r.thinking, false)
  assert.equal(hasThinkMarkup('plain answer'), false)
})

test('single block followed by answer', () => {
  const r = parseThinkBlocks('<think>reasoning</think>The answer is 42.')
  assert.equal(r.think, 'reasoning')
  assert.equal(r.answer, 'The answer is 42.')
  assert.equal(r.thinking, false)
})

test('open block without close marksw content as still thinking', () => {
  const r = parseThinkBlocks('<think>still reasoning')
  assert.equal(r.think, 'still reasoning')
  assert.equal(r.answer, '')
  assert.equal(r.thinking, true)
})

test('multiple blocks keep every segment and drop the tags (#3099)', () => {
  const raw = '<think>round one</think>partial answer<think>round two</think>final answer'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, 'round one\nround two')
  assert.equal(r.answer, 'partial answerfinal answer')
  assert.equal(r.thinking, false)
})

test('streaming growth: partial open tag text is still thinking', () => {
  // fullContent accumulates; after "<think>re" the parser must report thinking
  const r1 = parseThinkBlocks('<think>re')
  assert.equal(r1.thinking, true)
  assert.equal(r1.think, 're')
  assert.equal(r1.answer, '')
  // once the close tag arrives the answer flips out cleanly
  const r2 = parseThinkBlocks('<think>re</think>ans')
  assert.equal(r2.thinking, false)
  assert.equal(r2.think, 're')
  assert.equal(r2.answer, 'ans')
})

test('multiline reasoning is preserved inside the block', () => {
  const r = parseThinkBlocks('<think>line1\nline2</think>\n\nvisible')
  assert.equal(r.think, 'line1\nline2')
  assert.equal(r.answer, '\n\nvisible')
})

test('empty and empty-ish inputs', () => {
  assert.deepEqual(parseThinkBlocks(''), { think: '', answer: '', thinking: false })
  assert.deepEqual(parseThinkBlocks('<think></think>'), { think: '', answer: '', thinking: false })
})

test('stray close tag without open stays in the answer text', () => {
  const r = parseThinkBlocks('oops </think> done')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, 'oops </think> done')
})

test('hasThinkMarkup detects partial states', () => {
  assert.equal(hasThinkMarkup('<think>abc'), true)
  assert.equal(hasThinkMarkup('abc</think>'), true)
  assert.equal(hasThinkMarkup('no tags'), false)
})

// --- code regions are literal (#3132): a long markdown answer that merely
// mentions the tags must not get its head chopped into the thinking card ---

test('think tags inside a fenced code block stay literal answer (#3132)', () => {
  const raw = 'Before\n\n```xml\n<think>demo</think>\n```\n\nAfter'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, '')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, raw)
})

test('real block before a fenced example is still parsed', () => {
  const raw = '<think>meta reasoning</think>See the example:\n\n```xml\n<think>sample</think>\n```\n'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, 'meta reasoning')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, 'See the example:\n\n```xml\n<think>sample</think>\n```\n')
})

test('unterminated fence consumes the rest and keeps tags literal', () => {
  const r = parseThinkBlocks('answer so far\n```bash\n<think>never a block')
  assert.equal(r.thinking, false)
  assert.equal(r.think, '')
  assert.equal(r.answer, 'answer so far\n```bash\n<think>never a block')
})

test('tilde fences are honoured', () => {
  const raw = '~~~\n<think>x</think>\n~~~\ndone'
  const r = parseThinkBlocks(raw)
  assert.equal(r.thinking, false)
  assert.equal(r.think, '')
  assert.equal(r.answer, raw)
})

test('closing fence may be longer than the opening fence', () => {
  const raw = '````md\n<think>a</think>\n`````\ntail'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, '')
  assert.equal(r.answer, raw)
})

test('think tags inside an inline code span stay literal', () => {
  const r = parseThinkBlocks('Wrap the tag as `<think>` like this.')
  assert.equal(r.think, '')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, 'Wrap the tag as `<think>` like this.')
})

test('first close tag ends the block even when the reasoning shows a fenced example', () => {
  // A literal </think> inside a fence within the reasoning is indistinguishable
  // from the real close; the first close wins and the rest stays answer text.
  const r = parseThinkBlocks('<think>draft ```md\n<think>nested</think>\n```\nmore</think>final')
  assert.equal(r.thinking, false)
  assert.equal(r.think, 'draft ```md\n<think>nested')
  assert.equal(r.answer, '\n```\nmore</think>final')
})

test('streaming growth: fence closing later lets the real block pop out', () => {
  const r1 = parseThinkBlocks('```xml\n<think>doc sample')
  assert.equal(r1.thinking, false)
  assert.equal(r1.think, '')
  const r2 = parseThinkBlocks('```xml\n<think>doc sample</think>\n```\n<think>real reasoning</think>Answer')
  assert.equal(r2.thinking, false)
  assert.equal(r2.think, 'real reasoning')
  assert.equal(r2.answer, '```xml\n<think>doc sample</think>\n```\nAnswer')
})

test('unclosed inline span consumes the rest without inventing a block', () => {
  const r = parseThinkBlocks('code `until the end <think>x')
  assert.equal(r.thinking, false)
  assert.equal(r.think, '')
  assert.equal(r.answer, 'code `until the end <think>x')
})

test('indented fence (up to 3 spaces) is still a fence', () => {
  const raw = 'text\n   ```\n<think>kept literal</think>\n   ```\nend'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, '')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, raw)
})
