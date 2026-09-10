<template>
  <div class="sandbox-browser">
    <div class="browser-toolbar">
      <div class="browser-status" :class="{ 'is-live': stream.connected.value }">
        <span class="status-dot" aria-hidden="true" />
        <span>{{ t(stream.connected.value ? 'sandboxBrowser.live' : supportsStream() ? 'sandboxBrowser.connecting' : 'sandboxBrowser.snapshot') }}</span>
        <span class="status-divider" aria-hidden="true">/</span>
        <span>{{ t(controlling ? 'sandboxBrowser.controlling' : 'sandboxBrowser.viewOnly') }}</span>
      </div>
      <div class="browser-toolbar-actions">
      <t-tooltip :content="t(actualSize ? 'sandboxBrowser.fitWidth' : 'sandboxBrowser.actualSize')">
        <t-button theme="default" variant="text" size="small" :disabled="!frame?.image" :aria-pressed="actualSize" :aria-label="t(actualSize ? 'sandboxBrowser.fitWidth' : 'sandboxBrowser.actualSize')" @click="actualSize = !actualSize">
          {{ actualSize ? '100%' : t('sandboxBrowser.fit') }}
        </t-button>
      </t-tooltip>
      <t-button :theme="controlling ? 'default' : 'primary'" :variant="controlling ? 'outline' : 'base'" size="small" :loading="controlBusy"
        :disabled="!frame?.image || browserState !== 'running' || navigationBusy || (supportsStream() && !stream.connected.value)" @click="toggleControl">
        {{ t(controlling ? 'sandboxBrowser.release' : 'sandboxBrowser.takeControl') }}
      </t-button>
      </div>
    </div>
    <form v-if="frame?.image" class="browser-address" @submit.prevent="navigate()">
      <t-tooltip :content="t('sandboxBrowser.back')">
        <t-button theme="default" variant="text" shape="square" :aria-label="t('sandboxBrowser.back')" :disabled="!canInteract || navigationBusy" @click="navigate('back')"><t-icon name="chevron-left" /></t-button>
      </t-tooltip>
      <t-tooltip :content="t('sandboxBrowser.reload')">
        <t-button theme="default" variant="text" shape="square" :aria-label="t('sandboxBrowser.reload')" :disabled="!canInteract || navigationBusy" @click="navigate('reload')"><t-icon name="refresh" /></t-button>
      </t-tooltip>
      <t-input v-model="address" class="address-field" :readonly="!canInteract" :aria-label="t('sandboxBrowser.address')"
        @focus="addressEditing = true" @blur="finishAddressEdit" @change="addressDirty = true" @keydown.esc="resetAddress">
        <template #prefix-icon><t-icon name="internet" /></template>
      </t-input>
      <t-button type="submit" theme="default" variant="text" shape="square" :loading="navigationBusy" :aria-label="t('sandboxBrowser.go')" :disabled="!canInteract || !address.trim()"><t-icon name="arrow-right" /></t-button>
    </form>
    <div class="browser-guidance">
      <span>{{ t(controlling ? 'sandboxBrowser.controlHint' : 'sandboxBrowser.viewHint') }}</span>
      <span v-if="controlling && !supportsPointer()">{{ t('sandboxBrowser.pointerUpgradeHint') }}</span>
    </div>
    <div v-if="error" class="browser-error" role="status"><t-icon name="info-circle" /><span>{{ error }}</span></div>
    <div class="browser-stage">
      <div v-if="frame?.image" class="browser-viewport" :class="{ 'has-control': canInteract }" :style="{ cursor: viewportCursor, width: actualSize ? `${frame.width || 1280}px` : '100%', maxWidth: actualSize ? undefined : `${frame.width || 1280}px` }"
        :tabindex="canInteract ? 0 : -1" :aria-label="t('sandboxBrowser.viewport')"
        @focus.self="textInput.focusAt()" @wheel="onWheel">
        <div v-if="browserState !== 'running'" class="browser-reconnect">
          <span>{{ stateHint }}</span>
          <t-button v-if="browserState !== 'unavailable'" theme="default" variant="outline" size="small" :loading="controlBusy" @click="send('start')">{{ startLabel }}</t-button>
        </div>
        <img :src="`data:image/jpeg;base64,${frame.image}`" :alt="t('sandboxBrowser.viewport')" draggable="false" @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="onPointerUp" @pointercancel="onPointerCancel" @lostpointercapture="onPointerCancel" @pointerleave="cancelHover" />
        <textarea :ref="el => textInput.element.value = el as HTMLTextAreaElement | null" class="browser-text-target"
          :class="{ 'is-composing': textInput.composing.value }" :style="textInput.style.value" :disabled="!canInteract"
          :aria-label="t('sandboxBrowser.textLabel')" tabindex="-1" rows="1" autocomplete="off" autocapitalize="off" :spellcheck="false"
          @keydown.stop="textInput.onKey" @input="textInput.onInput" @beforeinput="textInput.onBeforeInput" @paste="textInput.onPaste"
          @compositionstart="textInput.onCompositionStart" @compositionupdate="textInput.onCompositionUpdate" @compositionend="textInput.onCompositionEnd" @blur="textInput.onBlur" />
      </div>
      <div v-else class="browser-empty">
        <t-icon name="internet" size="30px" />
        <p>{{ stateHint }}</p>
        <t-button v-if="browserState !== 'unavailable'" theme="default" variant="outline" :loading="controlBusy" @click="send('start')">{{ startLabel }}</t-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useBrowserTextInput } from '@/composables/useBrowserTextInput'
import { useBrowserStream } from '@/composables/useBrowserStream'
import { useI18n } from 'vue-i18n'
import { post } from '@/utils/request'

type BrowserState = 'running' | 'paused' | 'not_bound' | 'not_started' | 'unavailable' | 'error'
interface Frame { ok: boolean; state?: BrowserState; capabilities?: string[]; error?: string; image?: string; url?: string; revision?: number; width?: number; height?: number; controlled?: boolean }
const props = defineProps<{ sessionId: string; agentId?: string; agentSourceTenantId?: string | number | null }>()
const { t } = useI18n()
const frame = ref<Frame | null>(null)
const controlling = ref(false)
const actualSize = ref(false)
const controlPending = ref(0)
const navigationPending = ref(0)
const controlBusy = computed(() => controlPending.value > 0)
const navigationBusy = computed(() => navigationPending.value > 0)
const busy = computed(() => controlBusy.value || navigationBusy.value)
const addressEditing = ref(false)
const addressDirty = ref(false)
const remoteCursor = ref('crosshair')
const dragging = ref(false)
const browserState = ref<BrowserState>('not_started')
const stateHint = computed(() => t(browserState.value === 'paused' ? 'sandboxBrowser.pausedHint' : browserState.value === 'unavailable' ? 'sandboxBrowser.unavailableHint' : browserState.value === 'error' ? 'sandboxBrowser.reconnectHint' : 'sandboxBrowser.emptyHint'))
const startLabel = computed(() => t(browserState.value === 'paused' ? 'sandboxBrowser.resume' : 'sandboxBrowser.start'))
const error = ref('')
const address = ref('')
let token = crypto.randomUUID()
let stopped = false
let inFlight = false
let mayHoldControl = false
let timer: ReturnType<typeof setTimeout> | undefined
let lastHeartbeat = 0
let reconnectAt = 0
let reconnectDelay = 1000
const supportsStream = () => frame.value?.capabilities?.includes('stream') === true
const stream = useBrowserStream({
  sessionId: props.sessionId, token: () => token,
  async onFrame(message, isCurrent) {
    if (stopped || !message.data) return
    const image = new Image()
    image.src = `data:image/jpeg;base64,${message.data}`
    await image.decode()
    if (stopped || document.hidden || !isCurrent()) return
    frame.value = { ...frame.value, ok: true, image: message.data, width: message.metadata?.deviceWidth || 1280, height: message.metadata?.deviceHeight || 800 }
    browserState.value = 'running'
    error.value = ''
    reconnectDelay = 1000
    await nextTick()
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  },
  onURL(url) {
    if (frame.value) frame.value.url = url
    syncAddress(url)
    // Navigation may finish after a click reply. Refresh the controller's
    // revision from this event so the next input does not use the old page.
    if (controlling.value) void send('frame', {}, true)
  },
  onError(message) { error.value = message },
  onDisconnect() {
    token = crypto.randomUUID()
    mayHoldControl = false
    controlling.value = false
    browserState.value = 'error'
    pointerPending = null
    pointerId = null
    dragging.value = false
    cancelHover()
    reconnectAt = Date.now() + reconnectDelay
    reconnectDelay = Math.min(30000, reconnectDelay * 2)
  },
})
const canInteract = computed(() => controlling.value && browserState.value === 'running' && !controlBusy.value && (!supportsStream() || stream.connected.value))
const textInput = useBrowserTextInput({
  enabled: () => canInteract.value && !navigationBusy.value,
  send: (action, fields) => { void send(action, fields, true) },
  onTooLong: () => { error.value = t('sandboxBrowser.textTooLong') },
})
const viewportCursor = computed(() => !canInteract.value ? 'default' : dragging.value ? 'grabbing' : remoteCursor.value)
const endpoint = `/api/v1/sessions/${encodeURIComponent(props.sessionId)}/sandbox/browser`

async function command(action: string, fields: Record<string, unknown> = {}) {
  if (stream.connected.value) return stream.command({ action, revision: frame.value?.revision ?? 0, ...fields })
  const params = action === 'start' ? { agent_id: props.agentId, agent_source_tenant_id: props.agentSourceTenantId || undefined } : undefined
  const response = await post<{ data: Frame }>(endpoint, { action, token, revision: frame.value?.revision ?? 0, ...fields }, { params, timeout: action === 'start' ? 125000 : 45000 })
  return response.data
}
let queue: Promise<unknown> = Promise.resolve()
function send(action: string, fields: Record<string, unknown> = {}, background = false): Promise<boolean> {
  if (stopped) return Promise.resolve(false)
  // Continuous input never changes the toolbar's disabled/loading state.
  const pending = !background && ['start', 'acquire', 'release'].includes(action) ? controlPending
    : !background && ['open', 'back'].includes(action) ? navigationPending : undefined
  if (pending) pending.value++
  if (action === 'acquire') mayHoldControl = true
  const result = queue.then(() => perform(action, fields)).finally(() => {
    if (pending) pending.value--
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
      browserState.value = next.state || (stream.connected.value ? 'running' : 'error')
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
      frame.value = next
    }
    if (!next.image) frame.value = { ...frame.value, ...next }
    if (next.url) syncAddress(next.url)
    browserState.value = 'running'
    if (supportsStream() && !document.hidden && Date.now() >= reconnectAt) void stream.connect()
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

function syncAddress(url: string) {
  if (!addressEditing.value && !addressDirty.value) address.value = url
}
function finishAddressEdit() { addressEditing.value = false; if (!addressDirty.value) resetAddress() }
function resetAddress() { addressDirty.value = false; address.value = frame.value?.url || '' }
async function navigate(action: 'open' | 'back' | 'reload' = 'open') {
  if (!canInteract.value || navigationBusy.value) return
  const url = action === 'reload' ? frame.value?.url : address.value.trim()
  if (action !== 'back' && !url) return
  const draft = address.value
  cancelHover()
  wheelDelta = 0
  if (await send(action === 'back' ? 'back' : 'open', action === 'back' ? {} : { url }) && address.value === draft) resetAddress()
}
function toggleControl() { cancelHover(); wheelDelta = 0; void send(controlling.value ? 'release' : 'acquire') }
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
  if (!canInteract.value || busy.value || pointerId !== null || gestureEnding || event.button !== 0) return
  event.preventDefault()
  cancelHover()
  pointerId = event.pointerId
  pointerStart = pointerLast = pointFor(event)
  moved = false
  const target = event.currentTarget as HTMLImageElement
  target.setPointerCapture(event.pointerId)
  textInput.reset()
  textInput.focusAt(pointerStart.x / (frame.value?.width || 1280) * 100, pointerStart.y / (frame.value?.height || 800) * 100)
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
  if (pointerId === null) queueHover(pointFor(event))
  if (event.pointerId !== pointerId || !pointerStart) return
  event.preventDefault()
  pointerLast = pointFor(event)
  if (Math.hypot(pointerLast.x - pointerStart.x, pointerLast.y - pointerStart.y) > 3) { moved = true; dragging.value = true }
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
  } finally { dragging.value = false; gestureEnding = false; pointerStart = pointerLast = null }
}
function onPointerUp(event: PointerEvent) {
  if (event.pointerId !== pointerId) return
  event.preventDefault()
  void finishPointer(false, pointFor(event))
}
function onPointerCancel(event: PointerEvent) {
  if (event.pointerId === pointerId) void finishPointer(true, pointerLast)
}

// The preview image has no DOM hit testing. Ask the sandbox for the hovered
// element's cursor over the existing stream, with at most one lookup in flight.
const allowedCursors = new Set(['auto', 'default', 'pointer', 'text', 'vertical-text', 'crosshair', 'move', 'grab', 'grabbing', 'not-allowed', 'no-drop', 'wait', 'progress', 'help', 'context-menu', 'cell', 'copy', 'alias', 'all-scroll', 'col-resize', 'row-resize', 'n-resize', 'e-resize', 's-resize', 'w-resize', 'ne-resize', 'nw-resize', 'se-resize', 'sw-resize', 'ew-resize', 'ns-resize', 'nesw-resize', 'nwse-resize', 'zoom-in', 'zoom-out'])
let hoverPoint: Point | null = null
let hoverTimer: ReturnType<typeof setTimeout> | undefined
let hoverInFlight = false
let hoverGeneration = 0
function cancelHover() {
  hoverGeneration++
  hoverPoint = null
  clearTimeout(hoverTimer)
  hoverTimer = undefined
  remoteCursor.value = 'crosshair'
}
function queueHover(point: Point) {
  if (!canInteract.value || !stream.connected.value || !frame.value?.capabilities?.includes('cursor') || gestureEnding) return
  hoverPoint = point
  if (!hoverInFlight && !hoverTimer) hoverTimer = setTimeout(pumpHover, 80)
}
async function pumpHover() {
  hoverTimer = undefined
  if (!hoverPoint || !canInteract.value || pointerId !== null) return
  const point = hoverPoint, generation = hoverGeneration
  hoverPoint = null
  hoverInFlight = true
  const result = queue.then(async () => {
    if (generation !== hoverGeneration || !canInteract.value || pointerId !== null || !stream.connected.value) return
    const next = await stream.command({ action: 'hover', ...point })
    if (generation === hoverGeneration && next.ok && allowedCursors.has(next.cursor)) remoteCursor.value = next.cursor
  })
  queue = result.catch(() => {})
  try { await result } catch { /* Optional cursor feedback must not interrupt control. */ }
  finally {
    hoverInFlight = false
    if (hoverPoint && canInteract.value) hoverTimer = setTimeout(pumpHover, 80)
  }
}

// One scroll in flight and one accumulated delta; trackpad bursts cannot
// build a long queue that keeps moving after the user stops or releases control.
let wheelDelta = 0
let wheelPump: Promise<void> | null = null
function onWheel(event: WheelEvent) {
  if (!canInteract.value || navigationBusy.value || pointerId !== null || gestureEnding) return
  // The full-size image may extend beyond the drawer. Keep horizontal
  // trackpad gestures and Shift+wheel available for local panning.
  if (actualSize.value && (event.shiftKey || Math.abs(event.deltaX) > Math.abs(event.deltaY))) return
  event.preventDefault()
  const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? (frame.value?.height || 800) : 1
  wheelDelta = Math.max(-1200, Math.min(1200, wheelDelta + event.deltaY * unit))
  if (wheelPump) return
  wheelPump = (async () => {
    while (wheelDelta && canInteract.value && !stopped) {
      const delta = wheelDelta
      wheelDelta = 0
      if (!await send('scroll', { delta }, true)) break
    }
  })().finally(() => { wheelPump = null; wheelDelta = 0 })
}
async function poll() {
  if (stopped) return
  clearTimeout(timer)
  if (!document.hidden && !busy.value && !inFlight) {
    if (controlling.value && Date.now() - lastHeartbeat > 10000) {
      await send('heartbeat', {}, true); lastHeartbeat = Date.now()
    }
    if (!stream.connected.value && !stream.connecting.value && pointerId === null && !gestureEnding) {
      if (Date.now() >= reconnectAt) await send('frame', { stream: supportsStream() }, true)
    }
  }
  if (!stopped) timer = setTimeout(poll, browserState.value === 'running' ? 2000 : 10000)
}
function onVisibilityChange() {
  if (document.hidden) {
    textInput.reset()
    cancelHover()
    wheelDelta = 0
    if (stream.connected.value || stream.connecting.value) { stream.disconnect(); token = crypto.randomUUID(); controlling.value = false; mayHoldControl = false }
    else if (mayHoldControl) void send('release', {}, true)
  } else { reconnectAt = 0; void poll() }
}
function cancelPointerOnBlur() { textInput.reset(); void finishPointer(true, pointerLast) }
onMounted(() => { window.addEventListener('blur', cancelPointerOnBlur); document.addEventListener('visibilitychange', onVisibilityChange); void poll() })
onBeforeUnmount(() => {
  stopped = true; textInput.reset(); cancelHover(); wheelDelta = 0; clearTimeout(timer)
  stream.disconnect()
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('blur', cancelPointerOnBlur)
  // Release even if an acquire response is still in flight. Socket requests
  // are serialized; a lost network connection falls back to the 35s lease.
  if (mayHoldControl) void queue.then(() => command('release')).catch(() => {})
})
</script>

<style scoped lang="less">
.sandbox-browser { display: flex; flex-direction: column; gap: 0; height: 100%; min-height: 0; overflow: hidden; color: var(--td-text-color-primary); }
.browser-toolbar, .browser-address { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.browser-toolbar { justify-content: space-between; gap: 12px; padding: 0 0 12px; }
.browser-toolbar-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }
.browser-status { display: flex; align-items: center; flex-wrap: wrap; gap: 7px; font-size: 12px; color: var(--td-text-color-secondary); }
.status-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--td-text-color-placeholder); flex-shrink: 0; }
.is-live .status-dot { background: var(--td-brand-color); }
.status-divider { color: var(--td-component-stroke); }
.browser-address { padding: 6px; gap: 4px; border: 1px solid var(--td-component-stroke); border-radius: 8px; background: var(--td-bg-color-secondarycontainer); }
.address-field { flex: 1; min-width: 0; }
.browser-address :deep(.t-input) { border-color: transparent; background: var(--td-bg-color-container); }
.browser-address :deep(.t-input--focused) { border-color: var(--td-brand-color); }
.browser-guidance { display: flex; flex-direction: column; gap: 4px; padding: 10px 2px; font-size: 12px; line-height: 1.6; color: var(--td-text-color-placeholder); flex-shrink: 0; }
.browser-stage { flex: 1; min-height: 120px; overflow: auto; overscroll-behavior: contain; display: flex; align-items: flex-start; border: 1px solid var(--td-component-stroke); border-radius: 8px; background: var(--td-bg-color-secondarycontainer); }
.browser-viewport { position: relative; width: 100%; flex-shrink: 0; overflow: hidden; background: #fff;
  img { display: block; width: 100%; touch-action: none; user-select: none; }
  &.has-control { outline-offset: -2px; }
  &:focus-visible, &:focus-within { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }
}
.browser-empty { display: flex; flex: 1; min-height: 100%; box-sizing: border-box; flex-direction: column; align-items: center; justify-content: center; gap: 16px; padding: 32px 20px; color: var(--td-text-color-placeholder); p { font-size: 13px; line-height: 1.8; text-align: center; margin: 0; } }
.browser-reconnect { position: absolute; top: 8px; left: 8px; right: 8px; z-index: 1; padding: 8px 10px; border: 1px solid var(--td-component-stroke); border-radius: 6px; background: var(--td-bg-color-container); display: flex; align-items: center; gap: 12px; justify-content: space-between; font-size: 12px; color: var(--td-text-color-secondary); }
.browser-error { display: flex; align-items: flex-start; gap: 6px; padding: 8px 10px; margin-bottom: 10px; border-radius: 6px; background: var(--td-error-color-1); font-size: 12px; line-height: 1.7; color: var(--td-error-color); overflow-wrap: anywhere; flex-shrink: 0; .t-icon { flex-shrink: 0; margin-top: 3px; } }
.browser-text-target { position: absolute; z-index: 2; height: 24px; padding: 0; border: 0; outline: none; resize: none; overflow: hidden; pointer-events: none; opacity: 0; font: 16px/24px sans-serif;
  &.is-composing { opacity: 1; padding: 0 4px; box-sizing: border-box; color: var(--td-text-color-primary); background: var(--td-bg-color-container); border-bottom: 2px solid var(--td-brand-color); border-radius: 2px; }
}
@media (max-width: 480px) { .browser-toolbar { gap: 8px; } .browser-status { gap: 4px; } }
</style>
