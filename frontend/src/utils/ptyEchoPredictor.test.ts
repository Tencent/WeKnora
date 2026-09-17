import assert from 'node:assert/strict'
import test from 'node:test'
import { createPtyEchoPredictor, looksLikeSecretPrompt } from './ptyEchoPredictor'

function collect() {
  const written: string[] = []
  const predictor = createPtyEchoPredictor((chunk) => {
    written.push(typeof chunk === 'string' ? chunk : new TextDecoder().decode(chunk))
  })
  return {
    predictor,
    text: () => written.join(''),
  }
}

test('echoes printable keystrokes locally and strips matching PTY echo in chunks', () => {
  const { predictor, text } = collect()
  predictor.onLocal('h')
  predictor.onLocal('i')
  assert.equal(text(), 'hi')
  predictor.onRemote(new TextEncoder().encode('h'))
  predictor.onRemote(new TextEncoder().encode('i'))
  assert.equal(text(), 'hi')
})

test('keeps the predicted line across Enter so the remote echo does not flash', () => {
  const { predictor, text } = collect()
  predictor.onLocal('h')
  predictor.onLocal('i')
  predictor.onLocal('\r')
  assert.equal(text(), 'hi')
  predictor.onRemote(new TextEncoder().encode('hi\r\n'))
  assert.equal(text(), 'hi\r\n')
})

test('rewinds the prediction when the PTY echo does not match', () => {
  const { predictor, text } = collect()
  predictor.onLocal('a')
  predictor.onRemote(new TextEncoder().encode('\x1b[32mready\x1b[0m\n'))
  const got = text()
  assert.match(got, /\ba|[\b]/)
  assert.equal(got.includes('ready'), true)
})

test('does not locally echo control sequences', () => {
  const { predictor, text } = collect()
  predictor.onLocal('\x1b[A')
  predictor.onLocal('\r')
  predictor.onLocal('\t')
  assert.equal(text(), '')
})

test('echoes CJK locally and strips the matching remote bytes', () => {
  const { predictor, text } = collect()
  predictor.onLocal('你')
  assert.equal(text(), '你')
  predictor.onRemote(new TextEncoder().encode('你'))
  assert.equal(text(), '你')
})

test('locally erases a predicted character on backspace', () => {
  const { predictor, text } = collect()
  predictor.onLocal('ab')
  predictor.onLocal('\x7f')
  assert.equal(text(), 'ab\b \b')
})

test('looksLikeSecretPrompt detects sudo and localized prompts', () => {
  assert.equal(looksLikeSecretPrompt('[sudo] password for alice: '), true)
  assert.equal(looksLikeSecretPrompt('Password:'), true)
  assert.equal(looksLikeSecretPrompt('请输入密码:'), true)
  assert.equal(looksLikeSecretPrompt('user@host:~$ '), false)
})

test('does not locally echo keystrokes after a password prompt', () => {
  const { predictor, text } = collect()
  predictor.onRemote(new TextEncoder().encode('[sudo] password for alice: '))
  predictor.onLocal('s')
  predictor.onLocal('e')
  predictor.onLocal('c')
  predictor.onLocal('r')
  predictor.onLocal('e')
  predictor.onLocal('t')
  assert.equal(text(), '[sudo] password for alice: ')
  predictor.onLocal('\r')
  predictor.onRemote(new TextEncoder().encode('\r\nalice@host:~$ '))
  predictor.onLocal('l')
  assert.equal(text().endsWith('l'), true)
})
