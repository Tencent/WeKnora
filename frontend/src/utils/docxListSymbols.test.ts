import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeDocxListSymbols } from './docxListSymbols';

function list(levelText: string, font = 'Symbol', format = 'bullet') {
  return { format, levelText, rStyle: { 'font-family': font, color: 'red' } };
}

function normalize(...items: ReturnType<typeof list>[]) {
  normalizeDocxListSymbols({ numberingPart: { domNumberings: items } });
}

test('maps Symbol bullets, arrows and mathematical markers using their actual meanings', () => {
  const items = [list('\uf0b7'), list('\uf0ae', '"Symbol"'), list('\uf0a5', 'symbol')];
  normalize(...items);
  assert.deepEqual(items.map(item => item.levelText), ['•', '→', '∞']);
  assert.deepEqual(items.map(item => item.rStyle['font-family']), ['Symbol', '"Symbol"', 'symbol']);
  assert.ok(items.every(item => item.rStyle.color === 'red'));
});

test('preserves ordered lists, Unicode bullets, other fonts and unmapped private glyphs', () => {
  const items = [
    list('%1.', 'Symbol', 'decimal'), list('•'), list('▪', 'Arial'),
    list('\uf0b7', 'Wingdings'), list('\uf0b7', 'Custom Symbol'),
    list('\uf000'), list('\uf0b7\uf0ff'), list('\uf8ff'),
  ];
  const before = structuredClone(items);
  normalize(...items);
  assert.deepEqual(items, before);
});

test('keeps embedded Symbol fonts and picture bullets intact', () => {
  const item = list('\uf0b7');
  normalizeDocxListSymbols({
    numberingPart: { domNumberings: [item] },
    fontTablePart: { fonts: [{ name: 'Symbol', embedFontRefs: [{ id: 'font1' }] }] },
  });
  const picture = { ...list('\uf0b7'), bullet: { src: 'picture1' } };
  normalizeDocxListSymbols({ numberingPart: { domNumberings: [picture] } });
  assert.equal(item.levelText, '\uf0b7');
  assert.equal(picture.levelText, '\uf0b7');
});

test('normalizes multiple list levels without changing numbering metadata or body text', () => {
  const items = [
    { ...list('\uf0b7'), id: '1', level: 0, pStyle: { 'margin-left': '36pt' } },
    { ...list('\uf0ae'), id: '1', level: 1, pStyle: { 'margin-left': '72pt' } },
    { ...list('%1.', 'Arial', 'decimal'), id: '2', start: 3 },
  ] as const;
  const document = { numberingPart: { domNumberings: items }, body: { text: '\uf0b7' } };
  normalizeDocxListSymbols(document);
  assert.equal(items[0].levelText, '•');
  assert.equal(items[1].levelText, '→');
  assert.equal(items[1].level, 1);
  assert.equal(items[1].pStyle?.['margin-left'], '72pt');
  assert.equal(items[2].start, 3);
  assert.equal(document.body.text, '\uf0b7');
  const once = structuredClone(document);
  normalizeDocxListSymbols(document);
  assert.deepEqual(document, once);
  normalizeDocxListSymbols({});
});
