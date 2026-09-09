import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const terminal = readFileSync(new URL('./SandboxTerminal.vue', import.meta.url), 'utf8')
const theme = readFileSync(new URL('../../../assets/theme/theme.css', import.meta.url), 'utf8')

test('xterm palette and PTY prompt use WeKnora brand green', () => {
  const prompt = readFileSync(new URL('../../../../../docker/sandbox-pty-prompt.sh', import.meta.url), 'utf8')
  assert.match(theme, /--td-brand-color-4: #07c05f/)
  assert.match(terminal, /brightGreen: '#07c05f'/)
  assert.match(terminal, /brightBlue: '#07c05f'/)
  assert.match(prompt, /\\033\[01;32m/)
  assert.match(prompt, /\\033\[01;34m/)
  assert.doesNotMatch(prompt, /\\033\[01;31m/)
  assert.match(prompt, /\]\\W\\\[/)
  assert.doesNotMatch(prompt, /\]\\w\\\[/)
})
