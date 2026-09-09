import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const terminal = readFileSync(new URL('./SandboxTerminal.vue', import.meta.url), 'utf8')
const theme = readFileSync(new URL('../../../assets/theme/theme.css', import.meta.url), 'utf8')
const prompt = readFileSync(new URL('../../../../../docker/sandbox-pty-prompt.sh', import.meta.url), 'utf8')

test('xterm palette and PTY prompt use WeKnora brand green', () => {
  assert.match(theme, /--td-brand-color-4: #07c05f/)
  assert.match(terminal, /brightGreen: '#07c05f'/)
  assert.match(terminal, /brightBlue: '#07c05f'/)
  assert.match(prompt, /\\033\[01;32m/)
  assert.match(prompt, /\\033\[01;34m/)
  assert.doesNotMatch(prompt, /\\033\[01;31m/)
  assert.match(prompt, /\]\\W\\\[/)
  assert.doesNotMatch(prompt, /\]\\w\\\[/)
})

test('PTY output attaches after the first fit so FitAddon cannot wipe the prompt', () => {
  const start = terminal.indexOf('function mountTerminal')
  const end = terminal.indexOf('function unmountTerminal')
  assert.ok(start >= 0 && end > start)
  const mount = terminal.slice(start, end)
  const fitAt = mount.indexOf('applyFit()')
  const outputAt = mount.indexOf('terminal.onOutput')
  assert.ok(fitAt >= 0, 'mountTerminal must fit xterm')
  assert.ok(outputAt >= 0, 'mountTerminal must attach PTY output')
  assert.ok(
    fitAt < outputAt,
    'FitAddon.fit() clears the renderer when cols/rows change; flushing the buffered prompt before that fit leaves an empty cursor until the next keystroke',
  )
  assert.match(terminal, /xterm\.refresh\(0,\s*xterm\.rows\s*-\s*1\)/)
})

test('empty PTY screen is nudged with readline clear-screen, not enter', () => {
  assert.match(terminal, /PTY_PROMPT_NUDGE/)
  assert.match(terminal, /schedulePromptNudge/)
  assert.match(terminal, /estimatePtySize/)
})
