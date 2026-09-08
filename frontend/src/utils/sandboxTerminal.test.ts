import assert from 'node:assert/strict'
import test from 'node:test'
import { SandboxTerminal, type TerminalSocket, type TerminalPhase, type TerminalEvent } from './sandboxTerminal'

class Socket implements TerminalSocket {
  binaryType: BinaryType = 'blob'
  readyState: WebSocket['readyState'] = 1
  onopen: WebSocket['onopen'] = null
  onmessage: WebSocket['onmessage'] = null
  onerror: WebSocket['onerror'] = null
  onclose: WebSocket['onclose'] = null
  sent: any[] = []
  closed = false
  send(data: string) { this.sent.push(JSON.parse(data)) }
  close() { this.closed = true; this.readyState = 3 }
  open() { this.onopen?.call(this as unknown as WebSocket, new Event('open')) }
  receive(data: unknown) { this.onmessage?.call(this as unknown as WebSocket, new MessageEvent('message', { data })) }
  frame(frame: object) { this.receive(JSON.stringify(frame)) }
  drop() { this.onclose?.call(this as unknown as WebSocket, {} as CloseEvent) }
}

function setup() {
  const scope = new AbortController()
  const sockets: Socket[] = []
  const urls: string[] = []
  const phases: TerminalPhase[] = []
  const output: Uint8Array[] = []
  const events: TerminalEvent[] = []
  const errors: unknown[] = []
  let tickets = 0
  const terminal = new SandboxTerminal({
    signal: scope.signal, apiBase: '/app/weknora/', pageUrl: 'https://example.com/platform/chat/s',
    ticket: async () => ({ ticket: `ticket-${++tickets}`, expires_in: 30, websocket_path: '/api/v1/sandbox-terminal' }),
    socket: url => { urls.push(url); const socket = new Socket(); sockets.push(socket); return socket },
    onPhase: phase => phases.push(phase), onOutput: bytes => output.push(bytes),
    onEvent: event => events.push(event), onError: error => errors.push(error),
  })
  return { scope, sockets, urls, phases, output, events, errors, terminal }
}

test('auth is the first frame, with no URL credentials; only the explicit command form can start a command', async t => {
  const h = setup()
  t.after(h.terminal.dispose)
  await h.terminal.connect()
  assert.deepEqual(h.urls, ['wss://example.com/app/weknora/api/v1/sandbox-terminal'])
  const socket = h.sockets[0]
  assert.equal(socket.binaryType, 'arraybuffer')
  assert.equal(h.terminal.command('too early'), false)
  assert.equal(h.terminal.stdin('idle'), false)
  socket.open()
  assert.deepEqual(socket.sent, [{ type: 'auth', ticket: 'ticket-1' }])
  socket.frame({ type: 'ready' })
  assert.equal(h.terminal.stdin('still idle'), false)
  assert.equal(h.terminal.command('printf one\nprintf two'), true)
  assert.equal(h.terminal.command('duplicate'), false)
  socket.frame({ type: 'started' })
  assert.equal(h.terminal.stdin('\x03'), true)
  assert.equal(h.terminal.resize(80, 24), true)
  assert.equal(h.terminal.interrupt(), true)
  socket.receive(new Uint8Array([0x1b, 0x5b, 0x33, 0x31, 0x6d, 0xff]).buffer)
  socket.frame({ type: 'exit', exit_code: 130, reason: 'interrupted' })
  assert.equal(h.terminal.phase, 'ready')
  assert.equal(h.terminal.stdin('after exit'), false)
  assert.deepEqual(socket.sent.slice(1), [
    { type: 'command', command: 'printf one\nprintf two' }, { type: 'stdin', data: '\x03' },
    { type: 'resize', cols: 80, rows: 24 }, { type: 'interrupt' },
  ])
  assert.deepEqual([...h.output[0]], [0x1b, 0x5b, 0x33, 0x31, 0x6d, 0xff])
})

test('disconnect never reconnects or replays a running command', async t => {
  const h = setup()
  t.after(h.terminal.dispose)
  await h.terminal.connect()
  h.sockets[0].open()
  h.sockets[0].frame({ type: 'ready' })
  h.terminal.command('side-effect')
  h.sockets[0].frame({ type: 'started' })
  h.sockets[0].drop()
  assert.equal(h.terminal.phase, 'disconnected')
  assert.equal(h.sockets.length, 1)
  assert.equal(h.terminal.command('side-effect'), false)
  await h.terminal.connect()
  h.sockets[1].open()
  h.sockets[1].frame({ type: 'ready' })
  assert.deepEqual(h.sockets[1].sent, [{ type: 'auth', ticket: 'ticket-2' }])
})

test('scope abort closes sockets, clears callbacks, and blocks future input and reconnects', async () => {
  const h = setup()
  await h.terminal.connect()
  const socket = h.sockets[0]
  socket.open()
  socket.frame({ type: 'ready' })
  h.scope.abort()
  assert.equal(socket.closed, true)
  assert.equal(socket.onmessage, null)
  assert.equal(socket.onopen, null)
  assert.equal(h.terminal.command('blocked'), false)
  await h.terminal.connect()
  assert.equal(h.sockets.length, 1)
})

test('closing while ticket issuance is pending cannot create a socket later', async () => {
  const scope = new AbortController()
  let issue!: (ticket: { ticket: string; expires_in: number; websocket_path: string }) => void
  let sockets = 0
  const terminal = new SandboxTerminal({
    signal: scope.signal, apiBase: '', pageUrl: 'http://localhost/',
    ticket: () => new Promise(resolve => { issue = resolve }),
    socket: () => { ++sockets; return new Socket() },
    onPhase() {}, onOutput() {}, onEvent() {}, onError() {},
  })
  const connecting = terminal.connect()
  scope.abort()
  issue({ ticket: 'late', expires_in: 30, websocket_path: '/api/v1/sandbox-terminal' })
  await connecting
  assert.equal(sockets, 0)
})

test('policy errors and malformed control frames close the connection and preserve the error', async t => {
  const h = setup()
  t.after(h.terminal.dispose)
  await h.terminal.connect()
  h.sockets[0].open()
  h.sockets[0].frame({ type: 'error', code: 'policy_denied', message: 'Workspace policy changed' })
  assert.equal(h.sockets[0].closed, true)
  assert.deepEqual(h.errors[0], { code: 'policy_denied', message: 'Workspace policy changed' })
  await h.terminal.connect()
  h.sockets[1].receive('invalid-json')
  assert.equal(h.terminal.phase, 'disconnected')
  assert.match(String(h.errors[1]), /Invalid terminal frame/)
})
