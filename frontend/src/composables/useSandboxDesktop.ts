import { onUnmounted, ref, type Ref } from 'vue'
import RFB from '@novnc/novnc'
import { post } from '@/utils/request'

export type SandboxDesktopStatus =
  | 'idle'
  /** 懒启动中：start-desktop.sh 通常要 3–8 秒。 */
  | 'starting'
  | 'connected'
  | 'disconnected'
  /** 501：后端不支持中继，或镜像里没有桌面。 */
  | 'unsupported'
  /** 409 DESKTOP_BUSY：本会话已有一个桌面连接。 */
  | 'busy'
  /** 503：镜像对但桌面没起来，可重试。 */
  | 'start_failed'
  /** 沙箱在连接期间被技能安装重建了。 */
  | 'rebuilt'
  /** 409 SANDBOX_NOT_BOUND：会话还没有沙箱。 */
  | 'needs_provision'
  /** 409 SANDBOX_PAUSED：会话有沙箱但已暂停，唤醒需要确认。 */
  | 'paused'
  | 'unauthorized'
  | 'error'

const RECONNECT_BASE_DELAY_MS = 1000
const RECONNECT_MAX_DELAY_MS = 30000
// 活动上报只在 opcode 解析降级时才是唯一信号，所以宁可稀疏也不要吵。
const ACTIVITY_REPORT_MIN_INTERVAL_MS = 30000

// X11 keysyms. Linux guests paste/copy with Ctrl, not the Super key a Mac
// Cmd+V would otherwise send through noVNC.
const XK_Control_L = 0xffe3
const XK_c = 0x0063
const XK_v = 0x0076

function sendGuestChord(client: RFB, letterKeysym: number, code: string) {
  client.sendKey(XK_Control_L, 'ControlLeft', true)
  client.sendKey(letterKeysym, code, true)
  client.sendKey(letterKeysym, code, false)
  client.sendKey(XK_Control_L, 'ControlLeft', false)
}

async function pasteLocalClipboard(client: RFB) {
  let text = ''
  try {
    text = await navigator.clipboard.readText()
  } catch {
    return
  }
  if (!text) return
  client.clipboardPasteFrom(text)
  sendGuestChord(client, XK_v, 'KeyV')
}

function attachDesktopClipboard(client: RFB, element: HTMLElement): () => void {
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.repeat || event.altKey || event.shiftKey) return
    if (!(event.ctrlKey || event.metaKey)) return
    const key = event.key.toLowerCase()
    if (key === 'v') {
      // Cmd+V: Super+V is a no-op on XFCE. Leave the event alone so Safari
      // still fires `paste` with clipboardData; onPaste does the RFB sync.
      if (!event.ctrlKey || event.metaKey) return
      event.preventDefault()
      event.stopPropagation()
      void pasteLocalClipboard(client)
      return
    }
    if (key !== 'c') return
    event.preventDefault()
    event.stopPropagation()
    sendGuestChord(client, XK_c, 'KeyC')
  }
  const onPaste = (event: ClipboardEvent) => {
    const active = document.activeElement
    if (active !== element && !element.contains(active)) return
    const text = event.clipboardData?.getData('text') ?? ''
    if (!text) return
    event.preventDefault()
    event.stopPropagation()
    client.clipboardPasteFrom(text)
    sendGuestChord(client, XK_v, 'KeyV')
  }
  const onClipboard = (event: Event) => {
    const text = (event as CustomEvent<{ text?: string }>).detail?.text
    if (typeof text !== 'string' || text === '') return
    void navigator.clipboard.writeText(text).catch(() => {
      // Permission denied: the desktop copy still happened; local OS clipboard
      // just does not follow.
    })
  }
  element.addEventListener('keydown', onKeyDown, true)
  document.addEventListener('paste', onPaste, true)
  client.addEventListener('clipboard', onClipboard)
  return () => {
    element.removeEventListener('keydown', onKeyDown, true)
    document.removeEventListener('paste', onPaste, true)
    client.removeEventListener('clipboard', onClipboard)
  }
}

function resolveWsBase(): string {
  const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '')
  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${protocol}://${window.location.host}${base}`
}

async function mintDesktopTicket(sessionId: string): Promise<string> {
  const res = await post<{ success?: boolean; data?: { ticket?: string } }>(
    `/api/v1/sessions/${encodeURIComponent(sessionId)}/sandbox/desktop-ticket`,
    {},
  )
  const ticket = res?.data?.ticket
  if (!ticket) throw new Error('missing desktop ticket')
  return ticket
}

/**
 * post() 拒绝值：非枚举 `$httpStatus` + gin `{ error: "<code>" }`。
 * 409 同时表示 DESKTOP_BUSY 和 SANDBOX_NOT_BOUND，必须看 error 才能区分。
 */
function statusFromHttpStatus(
  status: number,
  errorCode?: string,
): SandboxDesktopStatus {
  if (status === 401 || status === 403) return 'unauthorized'
  if (status === 409) {
    if (errorCode === 'SANDBOX_NOT_BOUND') return 'needs_provision'
    if (errorCode === 'SANDBOX_PAUSED') return 'paused'
    if (errorCode === 'DESKTOP_BUSY') return 'busy'
    return 'error'
  }
  if (status === 501) return 'unsupported'
  if (status === 503) return 'start_failed'
  return 'error'
}

function errorCodeFromRejected(err: unknown): string | undefined {
  if (typeof err !== 'object' || err === null) return undefined
  const error = (err as { error?: unknown }).error
  return typeof error === 'string' ? error : undefined
}

function httpStatusFromRejected(err: unknown): number | undefined {
  if (typeof err !== 'object' || err === null) return undefined
  const payload = err as {
    $httpStatus?: unknown
    status?: unknown
    response?: { status?: unknown }
  }
  if (typeof payload.$httpStatus === 'number') return payload.$httpStatus
  if (typeof payload.status === 'number') return payload.status
  if (typeof payload.response?.status === 'number') return payload.response.status
  return undefined
}

/**
 * Ticket POST 失败走 HTTP 状态码（statusFromHttpStatus）。Ensure 失败
 * （SANDBOX_NOT_BOUND / SANDBOX_PAUSED 等）在升级后用 close reason 下发，
 * 因为浏览器 WebSocket API 读不到握手 HTTP 状态。升级之后的可预期关闭
 * （SANDBOX_REBUILT / IDLE_DISCONNECTED 等）同样走 CloseEvent.reason，
 * 不是 noVNC 1.7 的 disconnect detail（只有 `{ clean }`）。
 */
function statusFromCloseReason(reason: string): SandboxDesktopStatus | null {
  switch (reason) {
    case 'DESKTOP_UNSUPPORTED': return 'unsupported'
    case 'DESKTOP_BUSY': return 'busy'
    case 'DESKTOP_START_FAILED': return 'start_failed'
    case 'SANDBOX_REBUILT': return 'rebuilt'
    case 'SANDBOX_NOT_BOUND': return 'needs_provision'
    case 'SANDBOX_PAUSED': return 'paused'
    case 'IDLE_DISCONNECTED': return 'idle'
    case 'AUTH_REVOKED': return 'unauthorized'
    default: return null
  }
}

export type SandboxDesktopSession = {
  status: Ref<SandboxDesktopStatus>
  connect: (target: HTMLElement, options?: { provision?: boolean }) => void
  disconnect: () => void
  reportActivity: () => void
  dispose: () => void
}

export function useSandboxDesktop(
  sessionId: Ref<string>,
  agentId: Ref<string | undefined>,
  agentSourceTenantId: Ref<string | number | null | undefined> = ref(undefined),
): SandboxDesktopSession {
  const status = ref<SandboxDesktopStatus>('starting')

  let rfb: RFB | null = null
  let target: HTMLElement | null = null
  let disposed = false
  let wanted = false
  let opening = false
  let reconnectAttempt = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let lastActivityReport = 0
  let everConnected = false
  let detachClipboard: (() => void) | null = null
  // 当前这次连接是否带创建意图。只由 connect(..., { provision: true }) 置真。
  let allowProvision = false

  function clearClipboardBridge() {
    if (detachClipboard) {
      detachClipboard()
      detachClipboard = null
    }
  }

  function clearReconnectTimer() {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function scheduleReconnect() {
    if (disposed || !wanted || !target) return
    clearReconnectTimer()
    // 自动重连不得复活已被回收或暂停的沙箱：只有用户点击才携带创建意图。
    allowProvision = false
    const delay = Math.min(
      RECONNECT_BASE_DELAY_MS * 2 ** reconnectAttempt,
      RECONNECT_MAX_DELAY_MS,
    )
    reconnectAttempt += 1
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      void openDesktop()
    }, delay)
  }

  function buildUrl(ticket: string): string {
    const query = new URLSearchParams({ ticket })
    if (allowProvision) {
      query.set('provision', '1')
      const agent = agentId.value
      if (agent && agent !== 'builtin-quick-answer') query.set('agent_id', agent)
      const sourceTenant = agentSourceTenantId.value
      if (sourceTenant != null && String(sourceTenant).trim() !== '') {
        query.set('agent_source_tenant_id', String(sourceTenant).trim())
      }
    }
    return `${resolveWsBase()}/api/v1/sessions/${encodeURIComponent(sessionId.value)}` +
      `/sandbox/desktop?${query.toString()}`
  }

  async function openDesktop() {
    if (disposed || !wanted || opening || rfb || !target) return
    const sid = sessionId.value
    if (!sid) return

    opening = true
    // 懒启动跑完要 3–8 秒，期间必须有明确反馈，否则用户以为卡死。
    status.value = 'starting'
    let socket: WebSocket | undefined
    try {
      const ticket = await mintDesktopTicket(sid)
      if (disposed || !wanted || !target) return

      socket = new WebSocket(buildUrl(ticket), ['binary'])
      // 必须用 addEventListener：noVNC Websock.attach 会覆盖 socket.onclose。
      let nativeCloseReason = ''
      socket.addEventListener('close', (event: CloseEvent) => {
        nativeCloseReason = typeof event.reason === 'string' ? event.reason : ''
      }, { once: true })
      if (disposed || !wanted || !target) {
        socket.close()
        return
      }

      const client = new RFB(target, socket, {
        // 故意不传 credentials：RFB 安全类型是 None，认证在 websockify 那一跳，
        // 由后端加 Authorization 头完成。
        // 不传 wsProtocols：子协议已在 new WebSocket(..., ['binary']) 里协商。
      })
      client.scaleViewport = true
      // 必须为 false。它同时是后端中继不解析 RFB opcode 251 的前提条件
      // （internal/handler/session/sandbox_desktop_rfb.go 的 rfbClientMessageSize）。
      // 为了「自适应分辨率」打开它，坏掉的是空闲判定而不是画面。
      client.resizeSession = false
      client.background = '#1e1e1e'
      client.focusOnClick = true
      clearClipboardBridge()
      detachClipboard = attachDesktopClipboard(client, target)

      client.addEventListener('connect', () => {
        if (rfb !== client) return
        everConnected = true
        status.value = 'connected'
        reconnectAttempt = 0
      })
      client.addEventListener('disconnect', () => {
        if (rfb === client) rfb = null
        if (disposed || !wanted) return
        const mapped = nativeCloseReason ? statusFromCloseReason(nativeCloseReason) : null
        if (mapped) {
          status.value = mapped
          if (mapped === 'idle' || mapped === 'needs_provision' || mapped === 'paused') return
          if (mapped !== 'start_failed') return
        } else if (!everConnected) {
          // Handshake HTTP 409/502 never reaches JS. Reconnecting here while
          // the first GET still holds the Redis slot is what the user sees
          // as a sticky DESKTOP_BUSY overlay.
          status.value = 'error'
          return
        } else {
          status.value = 'disconnected'
        }
        scheduleReconnect()
      })

      rfb = client
    } catch (err: unknown) {
      if (
        socket
        && (socket.readyState === WebSocket.CONNECTING || socket.readyState === WebSocket.OPEN)
      ) {
        socket.close()
      }
      rfb = null
      if (disposed || !wanted) return
      const httpStatus = httpStatusFromRejected(err)
      const errorCode = errorCodeFromRejected(err)
      status.value = httpStatus
        ? statusFromHttpStatus(httpStatus, errorCode)
        : 'error'
      if (status.value === 'start_failed') scheduleReconnect()
    } finally {
      opening = false
    }
  }

  const session: SandboxDesktopSession = {
    status,
    connect(element, options?: { provision?: boolean }) {
      if (disposed) return
      wanted = true
      target = element
      allowProvision = options?.provision === true
      reconnectAttempt = 0
      everConnected = false
      clearReconnectTimer()
      if (rfb) {
        const client = rfb
        rfb = null
        // Drop wanted so the leftover disconnect cannot scheduleReconnect
        // or overwrite paused/needs_provision before the new dial starts.
        wanted = false
        client.disconnect()
        wanted = true
      }
      void openDesktop()
    },
    disconnect() {
      wanted = false
      clearReconnectTimer()
      clearClipboardBridge()
      if (rfb) {
        const client = rfb
        rfb = null
        client.disconnect()
      }
      status.value = 'idle'
    },
    /**
     * 上报键鼠活动。它只在后端 opcode 解析降级（遇到未知 opcode）时才是唯一的
     * 活动来源，正常情况下是冗余的——这也是它不能作为主信号的原因：不上报就能
     * 白嫖沙箱 TTL。
     */
    reportActivity() {
      const now = Date.now()
      if (now - lastActivityReport < ACTIVITY_REPORT_MIN_INTERVAL_MS) return
      lastActivityReport = now
      void post(
        `/api/v1/sessions/${encodeURIComponent(sessionId.value)}/sandbox/desktop/activity`,
        {},
      ).catch(() => {
        // 上报失败不影响画面，也不该打扰用户。
      })
    },
    dispose() {
      disposed = true
      wanted = false
      clearReconnectTimer()
      clearClipboardBridge()
      if (rfb) {
        const client = rfb
        rfb = null
        client.disconnect()
      }
      target = null
    },
  }

  onUnmounted(() => session.dispose())
  return session
}
