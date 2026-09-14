import type { TerminalTicket } from '../api/sandbox-workbench'
import { workbenchSocketUrl } from './sandboxWorkbench'
import {
  terminalBinaryBytes,
  terminalStdinFrames,
  terminalTextBytes,
} from './sandboxTerminalFrames'

export const TERMINAL_MAX_JSON_FRAME_BYTES = 16 * 1024

export type TerminalPhase = 'disconnected' | 'connecting' | 'ready' | 'starting' | 'running'
export type TerminalEvent =
  | { type: 'ready'; limits?: { max_frame_bytes?: number } }
  | { type: 'started' | 'pong' }
  | { type: 'exit'; exit_code: number; reason?: string }
  | { type: 'error'; code: string; message: string }

export type TerminalSocket = Pick<WebSocket,
  'binaryType' | 'readyState' | 'onopen' | 'onmessage' | 'onerror' | 'onclose' | 'send' | 'close'>

const encoder = new TextEncoder()

interface Options {
  ticket: () => Promise<TerminalTicket>
  apiBase: string
  pageUrl: string
  signal: AbortSignal
  socket?: (url: string) => TerminalSocket
  onPhase: (phase: TerminalPhase) => void
  onOutput: (bytes: Uint8Array) => void
  onEvent: (event: TerminalEvent) => void
  onError: (error: unknown) => void
}

export class SandboxTerminal {
  phase: TerminalPhase = 'disconnected'
  private socket: TerminalSocket | null = null
  private generation = 0
  private heartbeat?: ReturnType<typeof setInterval>
  private readyTimeout?: ReturnType<typeof setTimeout>
  private lastSeen = 0
  private disposed = false
  private maxFrameBytes = TERMINAL_MAX_JSON_FRAME_BYTES

  constructor(private options: Options) {
    options.signal.addEventListener('abort', this.dispose, { once: true })
  }

  private setPhase(phase: TerminalPhase) {
    this.phase = phase
    this.options.onPhase(phase)
  }

  async connect(): Promise<void> {
    if (this.disposed || this.options.signal.aborted || this.phase !== 'disconnected') return
    const generation = ++this.generation
    this.setPhase('connecting')
    try {
      const ticket = await this.options.ticket()
      if (this.disposed || generation !== this.generation || this.options.signal.aborted) return
      if (!ticket.ticket || ticket.expires_in <= 0) throw new Error('Invalid terminal ticket')
      const url = workbenchSocketUrl(this.options.apiBase, ticket.websocket_path, this.options.pageUrl)
      const socket = (this.options.socket ?? (url => new WebSocket(url)))(url)
      this.socket = socket
      const current = () => !this.disposed && this.socket === socket && generation === this.generation
      socket.binaryType = 'arraybuffer'
      socket.onopen = () => {
        if (!current()) return
        socket.send(JSON.stringify({ type: 'auth', ticket: ticket.ticket }))
        ticket.ticket = ''
      }
      this.readyTimeout = setTimeout(() => {
        if (current() && this.phase === 'connecting') this.fail(new Error('Terminal authentication timed out'))
      }, 30_000)
      socket.onmessage = event => {
        if (!current()) return
        this.lastSeen = Date.now()
        if (event.data instanceof ArrayBuffer) {
          this.options.onOutput(new Uint8Array(event.data))
          return
        }
        if (typeof event.data !== 'string') return
        let frame: TerminalEvent
        try { frame = JSON.parse(event.data) } catch { this.fail(new Error('Invalid terminal frame')); return }
        if (!frame || typeof frame !== 'object') { this.fail(new Error('Invalid terminal frame')); return }
        if (frame.type === 'ready' && this.phase === 'connecting') {
          const advertised = frame.limits?.max_frame_bytes
          if (Number.isFinite(advertised) && (advertised as number) >= 256) {
            this.maxFrameBytes = Math.min(TERMINAL_MAX_JSON_FRAME_BYTES, Math.floor(advertised as number))
          }
          clearTimeout(this.readyTimeout)
          this.setPhase('ready')
          this.heartbeat = setInterval(() => {
            if (!current()) return
            if (Date.now() - this.lastSeen > 45_000) { this.fail(new Error('Terminal connection timed out')); return }
            this.send({ type: 'ping' })
          }, 15_000)
        } else if (frame.type === 'started') {
          this.setPhase('running')
        } else if (frame.type === 'exit') {
          this.setPhase('ready')
        } else if (frame.type === 'error') {
          this.options.onEvent(frame)
          this.fail({ code: frame.code, message: frame.message })
          return
        }
        this.options.onEvent(frame)
      }
      socket.onclose = () => { if (current()) this.disconnect() }
      socket.onerror = () => { if (current()) this.fail(new Error('Terminal connection failed')) }
    } catch (error) {
      if (generation === this.generation && !this.disposed) this.fail(error)
    }
  }

  command(command: string): boolean {
    if (this.phase !== 'ready' || !command.trim()) return false
    if (!this.send({ type: 'command', command })) return false
    this.setPhase('starting')
    return true
  }

  stdin(data: string): boolean {
    return this.sendInput(terminalTextBytes(data))
  }

  stdinBinary(data: string): boolean {
    return this.sendInput(terminalBinaryBytes(data))
  }

  interrupt(): boolean {
    return (this.phase === 'running' || this.phase === 'starting') && this.send({ type: 'interrupt' })
  }

  resize(cols: number, rows: number): boolean {
    if (this.phase === 'connecting' || cols < 1 || rows < 1) return false
    return this.send({ type: 'resize', cols, rows })
  }

  private send(frame: object): boolean {
    if (this.disposed || this.options.signal.aborted || this.socket?.readyState !== 1) return false
    try {
      const payload = JSON.stringify(frame)
      if (encoder.encode(payload).byteLength > this.maxFrameBytes) return false
      this.socket.send(payload)
      return true
    }
    catch (error) { this.fail(error); return false }
  }

  private sendInput(bytes: Uint8Array): boolean {
    if (this.phase !== 'running') return false
    const frames = terminalStdinFrames(bytes, this.maxFrameBytes)
    if (bytes.byteLength > 0 && frames.length === 0) return false
    return frames.every(frame => this.send(frame))
  }

  private fail(error: unknown) {
    this.disconnect()
    this.options.onError(error)
  }

  disconnect() {
    ++this.generation
    clearInterval(this.heartbeat)
    clearTimeout(this.readyTimeout)
    const socket = this.socket
    this.socket = null
    if (socket) {
      socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null
      socket.close()
    }
    this.setPhase('disconnected')
  }

  dispose = () => {
    this.disposed = true
    this.options.signal.removeEventListener('abort', this.dispose)
    this.disconnect()
  }
}
