import { onUnmounted, ref, type Ref } from 'vue'
import { post } from '@/utils/request'
import { readStoredPtyId, writeStoredPtyId } from '@/utils/sandboxPtyId'

export type SandboxTerminalStatus =
  /** 查询到会话沙箱已暂停；唤醒需要用户确认。 */
  | 'paused'
  | 'connecting'
  | 'ready'
  /** 查到会话没有运行中的沙箱；创建需要用户确认。 */
  | 'needs_provision'
  | 'exited'
  /** 用户已确认创建，但后端无处可建（智能体没配沙箱后端）。 */
  | 'no_sandbox'
  | 'unsupported'
  | 'idle'
  | 'unauthorized'
  | 'error'

export type SandboxTerminalControlFrame = {
  type: string
  code?: string
  message?: string
  pty_id?: number
  backend?: string
  exit_code?: number | null
  cols?: number
  rows?: number
}

const RECONNECT_BASE_DELAY_MS = 1000
const RECONNECT_MAX_DELAY_MS = 30000
const APP_PING_INTERVAL_MS = 25000
const PENDING_OUTPUT_MAX_BYTES = 1024 * 1024
// 服务端对单个输入帧设了 4 KiB 上限（terminalMaxInputBytes）；gorilla 超限时
// 直接关闭连接，而 xterm 的 paste 是整段进 onData 的，粘一段脚本就会掉线。
// 按字节切片后再发：PTY 是字节流，切在多字节字符中间也会被原样按序重组。
const INPUT_FRAME_MAX_BYTES = 2048

function resolveWsBase(): string {
  const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '')
  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${protocol}://${window.location.host}${base}`
}

async function mintTerminalTicket(sessionId: string): Promise<string> {
  const res = await post<{ success?: boolean; data?: { ticket?: string } }>(
    `/api/v1/sessions/${encodeURIComponent(sessionId)}/sandbox/terminal-ticket`,
    {},
  )
  const ticket = res?.data?.ticket
  if (!ticket) {
    throw new Error('missing terminal ticket')
  }
  return ticket
}

/**
 * provisionAttempted 决定 SANDBOX_NOT_BOUND 的含义：没带创建意图时只是"还没
 * 有沙箱，要不要建一个"，带了还失败才是真的无处可建。
 */
function statusFromErrorCode(
  code: string | undefined,
  provisionAttempted: boolean,
): SandboxTerminalStatus {
  if (code === 'SANDBOX_NOT_BOUND') {
    return provisionAttempted ? 'no_sandbox' : 'needs_provision'
  }
  if (code === 'SANDBOX_PAUSED') return 'paused'
  if (code === 'TERMINAL_UNSUPPORTED') return 'unsupported'
  if (code === 'IDLE_DISCONNECTED') return 'idle'
  if (code === 'AUTH_REVOKED') return 'unauthorized'
  return 'error'
}

export type SandboxTerminalSession = {
  status: Ref<SandboxTerminalStatus>
  /**
   * 由 SandboxTerminal.vue 注入：PTY 输出写入 xterm。
   * 在 handler 注册前到达的二进制帧会先入队，避免 bash 提示符在 xterm
   * 挂载前被丢弃。传入 null 可在卸载时重新开始缓冲。
   */
  onOutput: (handler: ((data: Uint8Array) => void) | null) => void
  /**
   * 连接并打开终端；已在连接中时幂等。
   *
   * provision 表示"这是用户的显式动作，允许后端创建或唤醒沙箱"。只有点击
   * 按钮才应该传 true：创建/唤醒沙箱是要计费的真实基础设施。打开面板时的
   * 自动连接一律不带：运行中的沙箱直接挂上，暂停或尚未创建则退回让用户确认。
   */
  connect: (options?: { provision?: boolean; cols?: number; rows?: number }) => void
  /** 发送键盘输入（xterm onData 的原始字符串）。 */
  sendInput: (data: string) => void
  /** 同步终端尺寸；ready 后调用。 */
  resize: (cols: number, rows: number) => void
  /** 断开并停止重连（组件卸载 / 会话切换时调用）。 */
  dispose: () => void
}

/**
 * 三个参数都必须是活的 ref（`toRef(props, …)`），不能是 `ref(props.x)` 那样的
 * 快照：每次 openSocket 都会重读它们，用户切换 agent（含共享来源空间）后的
 * 下一次连接才能按新 agent 的沙箱配置去创建沙箱。
 */
export function useSandboxTerminal(
  sessionId: Ref<string>,
  agentId: Ref<string | undefined>,
  agentSourceTenantId: Ref<string | number | null | undefined> = ref(undefined),
): SandboxTerminalSession {
  const status = ref<SandboxTerminalStatus>('connecting')

  let ws: WebSocket | null = null
  let opening = false
  let outputHandler: ((data: Uint8Array) => void) | null = null
  let pendingOutput: Uint8Array[] = []
  let pendingOutputBytes = 0
  let disposed = false
  let reconnectAttempt = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let pingTimer: ReturnType<typeof setInterval> | null = null
  let pendingResize: { cols: number; rows: number } | null = null
  let pendingGeometry: { cols: number; rows: number } | null = null
  // 刷新 / 关面板会拆掉这个闭包；PID 写在 sessionStorage，重挂时才能带 pty_id。
  let lastPid: number | null = readStoredPtyId(sessionId.value)
  // 当前这次连接是否带创建意图。只由 connect({ provision: true }) 置真。
  let allowProvision = false

  const textEncoder = new TextEncoder()

  function rememberPid(next: number | null) {
    lastPid = next
    writeStoredPtyId(sessionId.value, next)
  }

  function clearReconnectTimer() {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function clearPingTimer() {
    if (pingTimer !== null) {
      clearInterval(pingTimer)
      pingTimer = null
    }
  }

  function scheduleReconnect() {
    if (disposed) return
    clearReconnectTimer()
    // 自动重连不得复活已被回收的沙箱：只有用户点击才携带创建意图。掉线期间
    // 沙箱如果被回收，重连会拿到 needs_provision，由用户决定是否重建。
    allowProvision = false
    const delay = Math.min(
      RECONNECT_BASE_DELAY_MS * 2 ** reconnectAttempt,
      RECONNECT_MAX_DELAY_MS,
    )
    reconnectAttempt += 1
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      void openSocket()
    }, delay)
  }

  async function openSocket() {
    if (disposed || opening || ws) return
    const sid = sessionId.value
    if (!sid) return

    opening = true
    status.value = 'connecting'
    try {
      const ticket = await mintTerminalTicket(sid)
      if (disposed) return
      const query = new URLSearchParams({ ticket })
      if (allowProvision) {
        query.set('provision', '1')
        // agent_id 只在允许创建时才有意义：它是首次创建沙箱所用的配置来源。
        const agent = agentId.value
        if (agent && agent !== 'builtin-quick-answer') {
          query.set('agent_id', agent)
        }
        const sourceTenant = agentSourceTenantId.value
        if (sourceTenant != null && String(sourceTenant).trim() !== '') {
          query.set('agent_source_tenant_id', String(sourceTenant).trim())
        }
      }
      if (lastPid && lastPid > 0) {
        query.set('pty_id', String(lastPid))
      }
      if (pendingGeometry) {
        query.set('cols', String(pendingGeometry.cols))
        query.set('rows', String(pendingGeometry.rows))
      }
      const socket = new WebSocket(
        `${resolveWsBase()}/api/v1/sessions/${encodeURIComponent(sid)}/sandbox/terminal?${query.toString()}`,
      )
      if (disposed) {
        socket.close()
        return
      }
      ws = socket
    } catch {
      ws = null
      if (disposed) return
      status.value = 'error'
      scheduleReconnect()
      return
    } finally {
      opening = false
    }
    if (!ws) return
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      clearPingTimer()
      pingTimer = setInterval(() => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'ping' }))
        }
      }, APP_PING_INTERVAL_MS)
    }

    ws.onmessage = (event) => {
      if (typeof event.data === 'string') {
        handleControlFrame(event.data)
        return
      }
      const data =
        event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : new Uint8Array(0)
      deliverOutput(data)
    }

    ws.onclose = (event) => {
      clearPingTimer()
      ws = null
      if (disposed) return
      const reason = typeof event.reason === 'string' ? event.reason : ''
      if (
        event.code === 1008
        || reason === 'SANDBOX_NOT_BOUND'
        || reason === 'SANDBOX_PAUSED'
        || reason === 'TERMINAL_UNSUPPORTED'
        || reason === 'IDLE_DISCONNECTED'
        || reason === 'AUTH_REVOKED'
      ) {
        if (reason) {
          status.value = statusFromErrorCode(reason, allowProvision)
        } else if (status.value === 'ready' || status.value === 'connecting') {
          status.value = 'error'
        }
        // idle / unauthorized / paused：shell 还在沙箱里，PID 必须留下才能重挂。
        // 沙箱没了或后端不支持：PID 已无效，清掉以免下次 Create 撞上死进程。
        if (status.value !== 'idle' && status.value !== 'unauthorized' && status.value !== 'paused') {
          rememberPid(null)
        }
        return
      }
      if (
        status.value !== 'needs_provision'
        && status.value !== 'paused'
        && status.value !== 'no_sandbox'
        && status.value !== 'unsupported'
        && status.value !== 'exited'
        && status.value !== 'idle'
        && status.value !== 'unauthorized'
      ) {
        status.value = 'error'
        scheduleReconnect()
      }
    }

    ws.onerror = () => {
      // onclose 会随后触发，统一在那里处理。
    }
  }

  function handleControlFrame(raw: string) {
    let frame: SandboxTerminalControlFrame
    try {
      frame = JSON.parse(raw)
    } catch {
      return
    }
    switch (frame.type) {
      case 'ready':
        status.value = 'ready'
        rememberPid(typeof frame.pty_id === 'number' ? frame.pty_id : null)
        reconnectAttempt = 0
        // 创建意图到此为止。它只用来解释 SANDBOX_NOT_BOUND：连上之前是"还没
        // 有沙箱，要不要建"，连上之后再收到就一定是"沙箱被回收了"。不复位会
        // 把回收误判成 no_sandbox（"智能体没配后端"），文案和真实原因无关。
        allowProvision = false
        if (pendingResize) {
          sendResize(pendingResize.cols, pendingResize.rows)
          pendingResize = null
        }
        break
      case 'exited': {
        status.value = 'exited'
        rememberPid(null)
        break
      }
      case 'error': {
        status.value = statusFromErrorCode(frame.code, allowProvision)
        if (status.value !== 'idle' && status.value !== 'unauthorized' && status.value !== 'paused') {
          rememberPid(null)
        }
        break
      }
      default:
        break
    }
  }

  function deliverOutput(data: Uint8Array) {
    if (data.length === 0) return
    if (outputHandler) {
      outputHandler(data)
      return
    }
    pendingOutput.push(data)
    pendingOutputBytes += data.length
    while (pendingOutputBytes > PENDING_OUTPUT_MAX_BYTES && pendingOutput.length > 0) {
      const dropped = pendingOutput.shift()
      if (dropped) pendingOutputBytes -= dropped.length
    }
  }

  function sendRaw(frame: SandboxTerminalControlFrame) {
    ws?.send(JSON.stringify(frame))
  }

  function sendResize(cols: number, rows: number) {
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      pendingResize = { cols, rows }
      return
    }
    sendRaw({ type: 'resize', cols, rows })
  }

  const session: SandboxTerminalSession = {
    status,
    onOutput(handler) {
      outputHandler = handler
      if (!handler) return
      const queued = pendingOutput
      pendingOutput = []
      pendingOutputBytes = 0
      for (const chunk of queued) {
        handler(chunk)
      }
    },
    connect(options) {
      if (disposed || ws || opening) return
      // 丢掉待触发的自动重连，否则它稍后会带着 provision=false 再拨一次，
      // 和用户这次点击抢连接。
      clearReconnectTimer()
      allowProvision = options?.provision === true
      reconnectAttempt = 0
      const cols = options?.cols
      const rows = options?.rows
      pendingGeometry =
        cols && rows && cols > 0 && rows > 0 ? { cols, rows } : null
      void openSocket()
    },
    sendInput(data) {
      if (!ws || ws.readyState !== WebSocket.OPEN) return
      const bytes = textEncoder.encode(data)
      for (let offset = 0; offset < bytes.length; offset += INPUT_FRAME_MAX_BYTES) {
        ws.send(bytes.subarray(offset, offset + INPUT_FRAME_MAX_BYTES))
      }
    },
    resize: sendResize,
    dispose() {
      disposed = true
      clearReconnectTimer()
      clearPingTimer()
      if (ws) {
        const socket = ws
        ws = null
        socket.onclose = null
        socket.onerror = null
        socket.onmessage = null
        socket.close()
      }
    },
  }

  onUnmounted(() => session.dispose())

  return session
}
