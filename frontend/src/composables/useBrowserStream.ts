import { ref } from 'vue'
import { post } from '@/utils/request'

export function useBrowserStream(options: {
  sessionId: string
  token: () => string
  onFrame: (message: any, isCurrent: () => boolean) => Promise<void>
  onURL: (url: string) => void
  onDisconnect: () => void
  onError: (message: string) => void
}) {
  const connected = ref(false)
  const connecting = ref(false)
  let socket: WebSocket | null = null
  let generation = 0
  let nextId = 0
  let heartbeat: ReturnType<typeof setInterval> | undefined
  const pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>()

  function disconnect() {
    generation++
    clearInterval(heartbeat)
    const current = socket
    socket = null
    current?.close()
    connected.value = connecting.value = false
    for (const request of pending.values()) {
      clearTimeout(request.timer)
      request.reject(new Error('Browser stream disconnected'))
    }
    pending.clear()
  }

  async function connect() {
    if (socket || connecting.value) return
    connecting.value = true
    const currentGeneration = ++generation
    try {
      const response = await post<{ data: { ticket: string } }>(
        `/api/v1/sessions/${encodeURIComponent(options.sessionId)}/sandbox/browser-ticket`, {},
      )
      if (currentGeneration !== generation) return
      const ticket = response.data?.ticket
      if (!ticket) throw new Error('Missing browser stream ticket')
      const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '')
      const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
      const query = new URLSearchParams({ ticket, control_token: options.token() })
      const ws = new WebSocket(`${scheme}://${window.location.host}${base}/api/v1/sessions/${encodeURIComponent(options.sessionId)}/sandbox/browser/stream?${query}`)
      socket = ws
      const isCurrent = () => socket === ws && currentGeneration === generation
      const handshake = setTimeout(() => { if (isCurrent() && !connected.value) ws.close() }, 20000)
      ws.onmessage = async (event) => {
        if (!isCurrent()) return
        try {
          const message = JSON.parse(event.data)
          if (message.type === 'ready') {
            clearTimeout(handshake)
            connected.value = true
            connecting.value = false
          } else if (message.type === 'frame') {
            await options.onFrame(message, isCurrent)
            // ACK only after rendering, and only on the connection that sent it.
            if (isCurrent() && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'ack', seq: message.seq }))
          } else if (message.type === 'url' && typeof message.url === 'string') {
            options.onURL(message.url)
          } else if (message.type === 'result') {
            const request = pending.get(message.id)
            if (request) {
              clearTimeout(request.timer)
              pending.delete(message.id)
              request.resolve(message.data)
            }
          } else if (message.type === 'error') {
            options.onError(String(message.message || 'Browser stream failed'))
            ws.close()
          }
        } catch { ws.close() }
      }
      ws.onclose = () => {
        clearTimeout(handshake)
        if (!isCurrent()) return
        disconnect()
        options.onDisconnect()
      }
      ws.onerror = () => ws.close()
      heartbeat = setInterval(() => {
        if (isCurrent() && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'ping' }))
      }, 10000)
    } catch {
      if (currentGeneration === generation) {
        disconnect()
        options.onDisconnect()
      }
    }
  }

  function command(command: Record<string, unknown>): Promise<any> {
    const ws = socket
    if (!connected.value || !ws || ws.readyState !== WebSocket.OPEN) return Promise.reject(new Error('Browser stream disconnected'))
    const id = ++nextId
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id)
        reject(new Error('Browser command timed out'))
        ws.close()
      }, 45000)
      pending.set(id, { resolve, reject, timer })
      ws.send(JSON.stringify({ type: 'command', id, command }))
    })
  }
  return { connected, connecting, connect, disconnect, command }
}
