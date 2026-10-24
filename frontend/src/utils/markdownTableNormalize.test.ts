import assert from 'node:assert/strict';
import test from 'node:test';

import { normalizeSpuriousTablePrefixes } from './markdownTableNormalize.ts';

const brokenTable = '| | |\n| --- | --- |\n| Name | Value |\n| alpha | beta |';
const normalizedTable = '| Name | Value |\n| --- | --- |\n| alpha | beta |';

test('repairs spurious table prefixes outside code', () => {
  assert.equal(normalizeSpuriousTablePrefixes(brokenTable), normalizedTable);
  assert.equal(normalizeSpuriousTablePrefixes(normalizedTable), normalizedTable);
});

for (const fence of ['```', '~~~', '````', '~~~~']) {
  test(`preserves literal table examples inside ${fence} fences`, () => {
    const source = `${fence}markdown\n${brokenTable}\n${fence}`;
    assert.equal(normalizeSpuriousTablePrefixes(source), source);
  });
}

test('preserves indented code containing pipe-delimited rows', () => {
  for (const indent of ['    ', '\t']) {
    const source = brokenTable.split('\n').map(line => indent + line).join('\n');
    assert.equal(normalizeSpuriousTablePrefixes(source), source);
  }
});

test('resumes table repair after a code fence closes', () => {
  const code = `  ~~~markdown\n${brokenTable}\n  ~~~~  `;
  assert.equal(
    normalizeSpuriousTablePrefixes(`${code}\n\n${brokenTable}`),
    `${code}\n\n${normalizedTable}`,
  );
});

test('shorter, opposite-character and nonblank-suffix fences do not close code', () => {
  const source = `\`\`\`\`markdown\n${brokenTable}\n\`\`\`\n~~~\n\`\`\`\`suffix\n${brokenTable}\n\`\`\`\``;
  assert.equal(normalizeSpuriousTablePrefixes(source), source);
});

test('an unclosed fence protects the rest of the document', () => {
  const source = `~~~markdown\n${brokenTable}\n\n${brokenTable}`;
  assert.equal(normalizeSpuriousTablePrefixes(source), source);
});

test('a backtick in the info string does not start a backtick fence', () => {
  const source = `\`\`\`invalid\`info\n${brokenTable}`;
  assert.equal(normalizeSpuriousTablePrefixes(source), `\`\`\`invalid\`info\n${normalizedTable}`);
});
