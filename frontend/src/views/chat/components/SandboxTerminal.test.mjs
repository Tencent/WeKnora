import assert from 'node:assert/strict'
import test from 'node:test'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const terminal = readFileSync(new URL('./SandboxTerminal.vue', import.meta.url), 'utf8')
const theme = readFileSync(new URL('../../../assets/theme/theme.css', import.meta.url), 'utf8')
const promptUrl = new URL('../../../../../docker/sandbox-pty-prompt.sh', import.meta.url)
const prompt = readFileSync(promptUrl, 'utf8')
const promptPath = fileURLToPath(promptUrl)

test('xterm palette keeps prompt green but ls directories blue', () => {
  assert.match(theme, /--td-brand-color-4: #07c05f/)
  assert.match(terminal, /brightGreen: '#07c05f'/)
  assert.match(terminal, /brightBlue: '#729fcf'/)
  assert.doesNotMatch(terminal, /brightBlue: '#07c05f'/)
  assert.match(terminal, /brightCyan: '#34e2e2'/)
  assert.doesNotMatch(terminal, /brightCyan: '#08dd6e'/)
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

test('empty PTY screen is nudged with SIGWINCH, not keystrokes', () => {
  assert.match(terminal, /schedulePromptNudge/)
  assert.match(terminal, /estimatePtySize/)
  assert.match(terminal, /resize\(cols,\s*rows\s*-\s*1\)/)
  assert.doesNotMatch(terminal, /sendInput\(PTY_PROMPT_NUDGE\)/)
  assert.doesNotMatch(terminal, /sendInput\('\\x0c'\)/)
  assert.doesNotMatch(terminal, /sendInput\('\\r'\)/)
})

test('panel open looks up a running sandbox and only provisions on an explicit click', () => {
  assert.match(terminal, /connectLookup/)
  assert.match(terminal, /onMounted\(\(\) => \{\s*connectLookup\(\)/)
  assert.match(terminal, /connect\(\{ provision: false/)
  assert.match(terminal, /connect\(\{ provision: true/)
  assert.match(terminal, /status === 'paused'/)
  assert.match(terminal, /chat\.sandbox\.paused/)
  assert.doesNotMatch(terminal, /not_started/)
})

test('interactive bash defines Debian-style ls aliases', () => {
  const out = execFileSync('bash', [
    '--norc',
    '--noprofile',
    '-ic',
    `. '${promptPath}'; alias ll; alias la; alias l; alias ls; alias grep`,
  ], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  assert.match(out, /alias ll='ls -alF'/)
  assert.match(out, /alias la='ls -A'/)
  assert.match(out, /alias l='ls -CF'/)
  assert.match(out, /alias ls='ls --color=auto'/)
  assert.match(out, /alias grep='grep --color=auto'/)
})

test('interactive prompt script does not export PS1 or PROMPT_COMMAND', () => {
  assert.doesNotMatch(prompt, /export PS1/)
  assert.doesNotMatch(prompt, /export PROMPT_COMMAND/)
})
