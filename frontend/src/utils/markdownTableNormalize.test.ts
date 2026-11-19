import assert from 'node:assert/strict';
import test from 'node:test';
import { Marked } from 'marked';
import { normalizeSpuriousTablePrefixes } from './markdownTableNormalize.ts';

const marked = new Marked({ gfm: true });

for (const [label, header, expected] of [
  ['escaped pipe', String.raw`a\|b`, 'a|b'],
  ['three backslashes', String.raw`a\\\|b`, String.raw`a\|b`],
  ['escaped leading pipe', String.raw`\|a`, '|a'],
  ['escaped trailing pipe', String.raw`a\|`, 'a|'],
] as const) {
  test(`repaired imported table retains two columns with ${label}`, () => {
    const source = `| | |\n| --- | --- |\n| ${header} | Value |\n| item | 42 |`;
    const normalized = normalizeSpuriousTablePrefixes(source);
    assert.equal(normalized, `| ${header} | Value |\n| --- | --- |\n| item | 42 |`);
    const html = marked.parse(normalized) as string;
    assert.equal((html.match(/<th>/g) || []).length, 2);
    assert.equal((html.match(/<td>/g) || []).length, 2);
    assert.ok(html.includes(`<th>${expected}</th>`), html);
    assert.match(html, /<td>42<\/td>/);
  });
}

test('even backslashes leave a real delimiter', () => {
  const source = String.raw`| a\\| Value |
| item | 42 |`;
  const normalized = normalizeSpuriousTablePrefixes(source);
  assert.equal(normalized, String.raw`| a\\| Value |
| --- | --- |
| item | 42 |`);
  assert.equal(((marked.parse(normalized) as string).match(/<th>/g) || []).length, 2);
});

test('escaped terminal pipe without outer delimiter remains cell text', () => {
  const source = String.raw`| Name | a\|
| item | 42`;
  assert.equal(normalizeSpuriousTablePrefixes(source), String.raw`| Name | a\|
| --- | --- |
| item | 42`);
});

test('ordinary and already delimited tables are unchanged', () => {
  for (const source of [
    '| Name | Value |\n| --- | --- |\n| item | 42 |',
    String.raw`| a\|b | Value |
| :--- | ---: |
| item | 42 |`,
    'ordinary prose',
  ]) assert.equal(normalizeSpuriousTablePrefixes(source), source);
});

