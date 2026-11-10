import assert from 'node:assert/strict'
import test from 'node:test'
import {
  applyTextEdits,
  describeTextChange,
  isSecretLabel,
  markdownOutline,
  PageFillError,
  readLines,
} from './pageText.ts'

const doc = '# Title\n\nIntro line.\n\n## Setup\n\nRun it.\n'

test('edits patch the text in place and apply in order', () => {
  const out = applyTextEdits(doc, [
    { op: 'replace', old_text: 'Intro line.', text: 'A better intro.' },
    { op: 'insert_after', anchor: '## Setup\n', text: '\nInstall first.\n' },
    { op: 'insert_before', anchor: '# Title', text: '<!-- draft -->\n' },
    { op: 'append', text: '\n## Done\n' },
  ])
  assert.equal(out, '<!-- draft -->\n# Title\n\nA better intro.\n\n## Setup\n\nInstall first.\n\nRun it.\n\n## Done\n')
})

test('a later edit sees the text of the earlier ones', () => {
  const out = applyTextEdits('one', [
    { op: 'replace', old_text: 'one', text: 'two' },
    { op: 'replace', old_text: 'two', text: 'three' },
  ])
  assert.equal(out, 'three')
})

test('missing or repeated text fails with what to do, naming the edit', () => {
  assert.throws(() => applyTextEdits(doc, [{ op: 'replace', old_text: 'nope', text: 'x' }]),
    (err: Error) => err instanceof PageFillError && /not found; read the field again/.test(err.message))
  assert.throws(() => applyTextEdits('a b a', [
    { op: 'append', text: '!' },
    { op: 'replace', old_text: 'a', text: 'c' },
  ]), (err: Error) => err instanceof PageFillError && /^edit 2: old_text "a" occurs 2 times/.test(err.message))
  assert.throws(() => applyTextEdits(doc, [{ op: 'insert_after', text: 'x' }]), /insert_after needs anchor/)
  assert.throws(() => applyTextEdits(doc, [{ op: 'replace', old_text: '', text: 'x' }]), /replace needs old_text/)
})

test('CRLF in edits matches the field text', () => {
  assert.equal(applyTextEdits('a\nb\nc', [{ op: 'replace', old_text: 'a\r\nb', text: 'x\r\ny' }]), 'x\ny\nc')
})

test('replace_selection uses the selection, shifted by earlier edits', () => {
  const value = 'keep THIS keep'
  const sel = { start: 5, end: 9 }
  assert.equal(applyTextEdits(value, [{ op: 'replace_selection', text: 'that' }], sel), 'keep that keep')
  assert.equal(applyTextEdits(value, [
    { op: 'insert_before', anchor: 'keep THIS', text: '>> ' },
    { op: 'replace_selection', text: 'that' },
  ], sel), '>> keep that keep')
  assert.throws(() => applyTextEdits(value, [{ op: 'replace_selection', text: 'x' }]), /nothing is selected/)
  assert.throws(() => applyTextEdits(value, [{ op: 'replace_selection', text: 'x' }], { start: 3, end: 3 }), /nothing is selected/)
  assert.throws(() => applyTextEdits(value, [
    { op: 'replace', old_text: 'THIS', text: 'gone' },
    { op: 'replace_selection', text: 'x' },
  ], sel), /edit 2: nothing is selected/)
})

test('replace_selection keeps the line breaks around a selected line', () => {
  const value = '# 标题\n第一行\n第二行\n'
  const line = { start: 5, end: 9 } // "第一行\n"
  assert.equal(applyTextEdits(value, [{ op: 'replace_selection', text: 'Line one' }], line), '# 标题\nLine one\n第二行\n')
  const padded = { start: 4, end: 9 } // "\n第一行\n"
  assert.equal(applyTextEdits(value, [{ op: 'replace_selection', text: 'Line one' }], padded), '# 标题\nLine one\n第二行\n')
  assert.equal(applyTextEdits(value, [{ op: 'replace_selection', text: 'Line one\n\n' }], line), '# 标题\nLine one\n\n第二行\n')
  assert.equal(applyTextEdits('a\n\nb', [{ op: 'replace_selection', text: '' }], { start: 1, end: 3 }), 'ab')
})

test('readLines numbers lines and clamps the range', () => {
  const text = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n')
  assert.deepEqual(readLines(text, 9, 3), { text: ' 9| line 9\n10| line 10\n11| line 11', total: 12, last: 11 })
  assert.deepEqual(readLines(text, 11, 50), { text: '11| line 11\n12| line 12', total: 12, last: 12 })
  assert.equal(readLines(text, 99, 1).text, '12| line 12')
})

test('markdownOutline lists headings outside fenced code', () => {
  const md = '# A\ntext\n```bash\n# not a heading\n```\n## B ##\n~~~\n### hidden\n~~~\n### C'
  assert.deepEqual(markdownOutline(md), [
    { line: 1, level: 1, text: 'A' },
    { line: 6, level: 2, text: 'B' },
    { line: 10, level: 3, text: 'C' },
  ])
})

test('credential labels are spotted, token counts are not', () => {
  for (const label of ['API Key', 'api_key', 'Client Secret', 'Password', 'Access token', 'Bot Token', 'Cookie', 'API 密钥', '访问令牌', '登录密码']) {
    assert.equal(isSecretLabel(label), true, label)
  }
  for (const label of ['Max tokens', 'Token limit', 'Name', 'Base URL', 'Description', 'Keyword']) {
    assert.equal(isSecretLabel(label), false, label)
  }
  assert.equal(isSecretLabel(null, '', 'Secret'), true)
})

test('describeTextChange summarizes for the model and the card', () => {
  assert.equal(describeTextChange('old', ''), 'cleared')
  assert.equal(describeTextChange('', 'Hello'), 'set to "Hello"')
  assert.equal(describeTextChange('a', 'a\nb'), 'replaced the text (1 → 3 characters)')
  assert.equal(describeTextChange('abc', 'abcd', [
    { op: 'replace', old_text: 'a', text: 'a' },
    { op: 'insert_after', anchor: 'c', text: 'd' },
    { op: 'replace', old_text: 'b', text: 'b' },
  ]), '2 replace, 1 insert after (3 → 4 characters)')
})
