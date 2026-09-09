<template>
  <div class="sandbox-browser">
    <div class="browser-toolbar">
      <span class="browser-status">{{ t(controlling ? 'sandboxBrowser.controlling' : 'sandboxBrowser.viewOnly') }}</span>
      <t-button theme="default" variant="outline" size="small" :disabled="!frame?.image || browserState !== 'running' || busy" @click="toggleControl">
        {{ t(controlling ? 'sandboxBrowser.release' : 'sandboxBrowser.takeControl') }}
      </t-button>
    </div>
    <p class="browser-hint">{{ t('sandboxBrowser.previewHint') }}</p>
    <p v-if="controlling && !supportsPointer()" class="browser-hint">{{ t('sandboxBrowser.pointerUpgradeHint') }}</p>
    <form v-if="frame?.image" class="browser-address" @submit.prevent="send('open', { url: address })">
      <t-input v-model="address" :disabled="!controlling || busy" :aria-label="t('sandboxBrowser.address')" />
      <t-button type="submit" theme="default" variant="outline" :disabled="!controlling || busy">{{ t('sandboxBrowser.go') }}</t-button>
    </form>
    <div class="browser-error" role="status">{{ error }}</div>
    <div v-if="frame?.image" class="browser-viewport" :class="{ 'has-control': controlling }"
      :tabindex="controlling ? 0 : -1" :aria-label="t('sandboxBrowser.viewport')"
      @keydown="onKey" @wheel.prevent="onWheel">
      <div v-if="browserState !== 'running'" class="browser-reconnect">
        <span>{{ stateHint }}</span>
        <t-button v-if="browserState !== 'unavailable'" theme="default" variant="outline" size="small" :loading="busy" @click="send('start')">{{ startLabel }}</t-button>
      </div>
      <img :src="`data:image/jpeg;base64,${frame.image}`" :alt="t('sandboxBrowser.viewport')" draggable="false" @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="onPointerUp" @pointercancel="onPointerCancel" @lostpointercapture="onPointerCancel" />
    </div>
    <div v-else class="browser-empty">
      <t-icon name="internet" size="30px" />
      <p>{{ stateHint }}</p>
      <t-button v-if="browserState !== 'unavailable'" theme="default" variant="outline" :loading="busy" :disabled="busy" @click="send('start')">{{ startLabel }}</t-button>
    </div>
    <form v-if="controlling" class="browser-input" @submit.prevent="insertText">
      <t-input v-model="textInput" :placeholder="t('sandboxBrowser.textHint')" :disabled="busy" />
      <t-button type="submit" theme="default" variant="outline" :disabled="!textInput || busy">{{ t('sandboxBrowser.insert') }}</t-button>
    </form>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { post } from '@/utils/request'

type BrowserState = 'running' | 'paused' | 'not_bound' | 'not_started' | 'unavailable' | 'error'
interface Frame { ok: boolean; state?: BrowserState; capabilities?: string[]; error?: string; image?: string; url?: string; revision?: number; width?: number; height?: number; controlled?: boolean }
const props = defineProps<{ sessionId: string; agentId?: string; agentSourceTenantId?: string | number | null }>()
const { t } = useI18n()
const frame = ref<Frame | null>(null)
const controlling = ref(false)
const pendingActions = ref(0)
const busy = computed(() => pendingActions.value > 0)
const browserState = ref<BrowserState>('not_started')
const stateHint = computed(() => t(browserState.value === 'paused' ? 'sandboxBrowser.pausedHint' : browserState.value === 'unavailable' ? 'sandboxBrowser.unavailableHint' : browserState.value === 'error' ? 'sandboxBrowser.reconnectHint' : 'sandboxBrowser.emptyHint'))
const startLabel = computed(() => t(browserState.value === 'paused' ? 'sandboxBrowser.resume' : 'sandboxBrowser.start'))
const error = ref('')
const address = ref('')
const textInput = ref('')
const token = crypto.randomUUID()
let stopped = false
let inFlight = false
let mayHoldControl = false
let timer: ReturnType<typeof setTimeout> | undefined
let lastHeartbeat = 0
const endpoint = `/api/v1/sessions/${encodeURIComponent(props.sessionId)}/sandbox/browser`

async function command(action: string, fields: Record<string, unknown> = {}) {
  const params = action === 'start' ? { agent_id: props.agentId, agent_source_tenant_id: props.agentSourceTenantId || undefined } : undefined
  const response = await post<{ data: Frame }>(endpoint, { action, token, revision: frame.value?.revision ?? 0, ...fields }, { params, timeout: action === 'start' ? 125000 : 45000 })
  return response.data
}
let queue: Promise<unknown> = Promise.resolve()
function send(action: string, fields: Record<string, unknown> = {}, background = false): Promise<boolean> {
  if (stopped) return Promise.resolve(false)
  if (!background) pendingActions.value++
  if (action === 'acquire') mayHoldControl = true
  const result = queue.then(() => perform(action, fields)).finally(() => {
    if (!background) pendingActions.value--
  })
  queue = result.catch(() => {})
  return result
}
async function perform(action: string, fields: Record<string, unknown>) {
  if (stopped) return false
  inFlight = true
  try {
    const next = await command(action, fields)
    if (stopped) return false
    if (!next.ok) {
      browserState.value = next.state || 'error'
      error.value = ['paused', 'not_bound', 'not_started', 'unavailable'].includes(browserState.value)
        ? '' : next.error || t('sandboxBrowser.commandFailed')
      controlling.value = false
      return false
    }
    if (next.image) {
      // Decode before swapping the visible image. Polling must not blank the
      // viewport or remount controls while the next JPEG is loading.
      if (next.image !== frame.value?.image) {
        const image = new Image()
        image.src = `data:image/jpeg;base64,${next.image}`
        await image.decode()
      }
      if (stopped) return false
      const previousURL = frame.value?.url
      frame.value = next
      if (next.url && next.url !== previousURL) address.value = next.url
    }
    browserState.value = 'running'
    if (action === 'acquire') controlling.value = true
    if (action === 'release' || next.controlled === false) {
      controlling.value = false
      mayHoldControl = false
    }
    error.value = ''
    return true
  } catch (err: any) {
    if (!stopped) {
      error.value = err?.message || t('sandboxBrowser.commandFailed')
      browserState.value = 'error'
      controlling.value = false
    }
    return false
  } finally { inFlight = false }
}

function toggleControl() { void send(controlling.value ? 'release' : 'acquire') }
type Point = { x: number; y: number }
let pointerId: number | null = null
let pointerStart: Point | null = null
let pointerLast: Point | null = null
let pointerPending: Point | null = null
let pointerPump: Promise<void> | null = null
let gestureEnding = false
let moved = false
const supportsPointer = () => frame.value?.capabilities?.includes('pointer') === true
function pointFor(event: PointerEvent): Point {
  const rect = (event.currentTarget as HTMLImageElement).getBoundingClientRect()
  return {
    x: Math.max(0, Math.min(1280, (event.clientX - rect.left) / rect.width * (frame.value?.width || 1280))),
    y: Math.max(0, Math.min(800, (event.clientY - rect.top) / rect.height * (frame.value?.height || 800))),
  }
}
function onPointerDown(event: PointerEvent) {
  if (!controlling.value || busy.value || pointerId !== null || gestureEnding || event.button !== 0) return
  event.preventDefault()
  pointerId = event.pointerId
  pointerStart = pointerLast = pointFor(event)
  moved = false
  const target = event.currentTarget as HTMLImageElement
  target.setPointerCapture(event.pointerId)
  target.parentElement?.focus()
  if (supportsPointer()) void send('pointer', { phase: 'down', ...pointerStart }, true)
}
function pumpPointerMoves(): Promise<void> {
  if (pointerPump) return pointerPump
  pointerPump = (async () => {
    // Keep only the latest pending position while one remote move is in flight.
    // Do not enqueue hundreds of stale coordinates behind screenshots.
    while (pointerPending && !stopped) {
      const point = pointerPending
      pointerPending = null
      if (!await send('pointer', { phase: 'move', ...point }, true)) { pointerPending = null; break }
    }
  })().finally(() => { pointerPump = null })
  return pointerPump
}
function onPointerMove(event: PointerEvent) {
  if (event.pointerId !== pointerId || !pointerStart) return
  event.preventDefault()
  pointerLast = pointFor(event)
  if (Math.hypot(pointerLast.x - pointerStart.x, pointerLast.y - pointerStart.y) > 3) moved = true
  if (supportsPointer() && moved) { pointerPending = pointerLast; void pumpPointerMoves() }
}
async function finishPointer(cancel: boolean, point: Point | null) {
  if (pointerId === null) return
  pointerId = null
  gestureEnding = true
  try {
    if (supportsPointer()) {
      if (!cancel && moved && point) pointerPending = point
      else pointerPending = null
      await pumpPointerMoves()
      await send('pointer', { phase: cancel ? 'cancel' : 'up', ...(point || pointerLast || { x: 0, y: 0 }) }, true)
    } else if (!cancel) {
      if (moved) error.value = t('sandboxBrowser.pointerUpgradeHint')
      else if (point) await send('click', point, true)
    }
  } finally { gestureEnding = false; pointerStart = pointerLast = null }
}
function onPointerUp(event: PointerEvent) {
  if (event.pointerId !== pointerId) return
  event.preventDefault()
  void finishPointer(false, pointFor(event))
}
function onPointerCancel(event: PointerEvent) {
  if (event.pointerId === pointerId) void finishPointer(true, pointerLast)
}

function onWheel(event: WheelEvent) {
  if (controlling.value && pointerId === null && !gestureEnding) void send('scroll', { delta: Math.max(-1200, Math.min(1200, event.deltaY)) })
}
function onKey(event: KeyboardEvent) {
  if (!controlling.value || event.isComposing || pointerId !== null || gestureEnding) return
  let key = event.key
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === 'a') key = 'ControlOrMeta+A'
  else if (event.shiftKey && key === 'Tab') key = 'Shift+Tab'
  else if (event.ctrlKey || event.metaKey || event.altKey) return
  if (['Enter', 'Tab', 'Shift+Tab', 'Backspace', 'Delete', 'Escape', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'ControlOrMeta+A'].includes(key)) {
    event.preventDefault(); void send('press', { key })
  } else if (key.length === 1) {
    event.preventDefault(); void send('type', { text: key })
  }
}
async function insertText() {
  const text = textInput.value
  if (await send('type', { text })) textInput.value = ''
}
async function poll() {
  if (stopped) return
  if (!document.hidden && !busy.value && !inFlight) {
    if (controlling.value && Date.now() - lastHeartbeat > 10000) {
      await send('heartbeat', {}, true); lastHeartbeat = Date.now()
    }
    if (pointerId === null && !gestureEnding) await send('frame', {}, true)
  }
  if (!stopped) timer = setTimeout(poll, browserState.value === 'running' ? 2000 : 10000)
}
function cancelPointerOnBlur() { void finishPointer(true, pointerLast) }
onMounted(() => { window.addEventListener('blur', cancelPointerOnBlur); void poll() })
onBeforeUnmount(() => {
  stopped = true; clearTimeout(timer)
  window.removeEventListener('blur', cancelPointerOnBlur)
  // Release even if an acquire response is still in flight. Socket requests
  // are serialized; a lost network connection falls back to the 35s lease.
  if (mayHoldControl) void queue.then(() => command('release')).catch(() => {})
})
</script>

<style scoped lang="less">
.sandbox-browser { display: flex; flex-direction: column; gap: 12px; height: 100%; overflow-y: auto; color: var(--td-text-color-primary); }
.browser-toolbar, .browser-address, .browser-input { display: flex; align-items: center; gap: 8px; }
.browser-toolbar { justify-content: space-between; }
.browser-status { font-size: 12px; color: var(--td-text-color-secondary); }
.browser-hint { margin: 0; color: var(--td-text-color-placeholder); line-height: 1.7; font-size: 12px; }
.browser-viewport { position: relative; border: 1px solid var(--td-component-stroke); border-radius: 6px; overflow: hidden; background: #fff; flex-shrink: 0; img { display: block; width: 100%; touch-action: none; user-select: none; } &.has-control { cursor: default; } &:focus-visible { outline: 2px solid var(--td-text-color-secondary); outline-offset: 2px; } }
.browser-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 16px; padding: 50px 20px; color: var(--td-text-color-placeholder); p { font-size: 13px; line-height: 1.8; text-align: center; margin: 0; } }
.browser-reconnect { position: absolute; top: 8px; left: 8px; right: 8px; z-index: 1; padding: 8px 10px; border: 1px solid var(--td-component-stroke); border-radius: 6px; background: var(--td-bg-color-container); display: flex; align-items: center; gap: 12px; justify-content: space-between; font-size: 12px; color: var(--td-text-color-secondary); }
.browser-error { min-height: 20px; font-size: 12px; line-height: 1.7; color: var(--td-error-color); overflow-wrap: anywhere; }
</style>
