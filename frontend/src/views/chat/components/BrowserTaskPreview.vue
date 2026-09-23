<template>
  <Teleport :to="pipTarget || 'body'" :disabled="!pipTarget">
    <aside v-if="status.selected" ref="previewElement" class="browser-task-preview"
      :class="{ 'is-dragging': dragging, 'is-pip': !!pipTarget, 'needs-help': status.needs_help }"
      :style="pipTarget ? {} : positionStyle" :aria-label="t('localBrowser.preview')">
      <div class="preview-heading" @pointerdown="!pipTarget && startDrag($event)" @pointermove="moveDrag"
        @pointerup="stopDrag" @pointercancel="stopDrag" @lostpointercapture="stopDrag">
        <BrowserIcon class="preview-browser-icon" width="16" height="16" />
        <strong :title="pageAddress || t('localBrowser.controlScope')">{{ pageHost || t('localBrowser.local') }}</strong>
        <button v-if="!(status.needs_help && status.action === 'tab_borrow')" class="preview-popout"
          :disabled="!status.connected || !status.task_id || busy || status.stopping"
          :title="t('localBrowser.locateWindow')" :aria-label="t('localBrowser.locateWindow')"
          @pointerdown.stop @click="act('focus')"><t-icon name="jump" size="15px" /></button>
        <button v-if="pipSupported" class="preview-popout" :disabled="pipOpening"
          :title="t(pipTarget ? 'localBrowser.pipReturn' : 'localBrowser.pipOpen')"
          :aria-label="t(pipTarget ? 'localBrowser.pipReturn' : 'localBrowser.pipOpen')"
          @pointerdown.stop @click="togglePictureInPicture"><t-icon :name="pipTarget ? 'fullscreen-exit' : 'fullscreen'" size="15px" /></button>
      </div>
      <section v-if="status.needs_help" class="preview-handoff" role="status" aria-live="polite">
        <strong>{{ t('localBrowser.needHelp') }}</strong>
        <p v-if="status.help_prompt" class="handoff-prompt">{{ status.help_prompt }}</p>
        <p>{{ t(status.action === 'tab_borrow' ? 'localBrowser.borrowHint' : 'localBrowser.helpHint') }}</p>
        <t-button v-if="status.action !== 'tab_borrow'" class="handoff-locate" theme="default" variant="outline" size="small"
          :disabled="!status.connected || !status.task_id || busy || status.stopping" @click="act('focus')">
          <t-icon name="jump" />{{ t('localBrowser.locateWindow') }}
        </t-button>
      </section>
      <button v-if="!(status.needs_help && status.action === 'tab_borrow')" class="preview-image" :class="{ 'is-empty': !preview }"
        :disabled="!status.connected || !status.task_id || busy || status.stopping"
        :aria-label="t('localBrowser.locateWindow')" :title="t('localBrowser.locateWindow')" @click="act('focus')">
        <img v-if="preview" :src="preview" :alt="t('localBrowser.preview')" />
        <span v-else class="preview-empty"><BrowserIcon width="28" height="28" />{{ t(status.connected ? 'localBrowser.waiting' : 'localBrowser.reconnectShort') }}</span>
      </button>
      <p v-if="status.last_error" class="preview-error" role="alert">{{ status.last_error }}</p>
      <p v-if="pipFailed" class="preview-error" role="alert">{{ t('localBrowser.pipFailed') }}</p>
      <p v-if="error" class="preview-error" role="alert">{{ error }}</p>
      <div class="preview-toolbar" :aria-label="t('localBrowser.controlScope')">
        <span class="preview-status" :class="{ live: status.connected && !status.paused && !status.stopping && !status.needs_help && !status.idle && !previewStale, attention: status.needs_help || status.paused }"
          :title="statusDetail" role="status"><i /><span>{{ statusLabel }}</span></span>
        <div class="preview-actions">
          <button v-if="status.connected" class="preview-control"
            :aria-label="t(status.paused ? 'localBrowser.resume' : 'localBrowser.pause')"
            :title="t(status.paused ? 'localBrowser.resume' : 'localBrowser.pauseHint')"
            :disabled="busy || status.stopping" @click="act(status.paused ? 'resume' : 'pause')">
            <t-icon :name="status.paused ? 'play' : 'pause'" size="14px" />{{ t(status.paused ? 'localBrowser.resumeShort' : 'localBrowser.pauseShort') }}
          </button>
          <button v-else class="preview-control" @click="openBrowserSettings">{{ t('localBrowser.settingsTitle') }}</button>
          <button class="preview-control" :title="t('localBrowser.stopHint')" :aria-label="t('localBrowser.stop')"
            :disabled="busy || status.stopping" @click="act('stop')"><t-icon name="stop" size="14px" />{{ t('localBrowser.stopShort') }}</button>
        </div>
      </div>
    </aside>
  </Teleport>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { get, post } from '@/utils/request'
import { useRouter } from 'vue-router'
import { toolboxLocation } from '@/config/toolbox'
import { browserActionLabel, browserPageAddress } from '@/utils/browserToolDisplay'
import BrowserIcon from '@/components/icons/BrowserIcon.vue'
import { useFloatingPreviewDrag } from '@/composables/useFloatingPreviewDrag'
import { useDocumentPictureInPicture } from '@/composables/useDocumentPictureInPicture'
const props = defineProps<{ sessionId: string }>()
const { t } = useI18n(), router = useRouter()
const status = ref({ enabled: false, selected: false, connected: false, paused: false, idle: false, needs_help: false, help_prompt: '', task_id: '', action: '', action_elapsed_ms: 0, page_url: '', last_error: '', stopping: false })
const busy = ref(false), error = ref(''), preview = ref(''), previewStale = ref(false)
const pageAddress = computed(() => browserPageAddress(status.value.page_url))
const pageHost = computed(() => pageAddress.value ? new URL(pageAddress.value).hostname : '')
const statusLabel = computed(() => {
  const s = status.value
  if (!s.connected) return t('localBrowser.offline')
  if (s.stopping) return t('localBrowser.stopping')
  if (s.needs_help) return t('localBrowser.needHelp')
  if (s.paused) return t('localBrowser.paused')
  if (s.idle) return t('localBrowser.previewIdle')
  if (previewStale.value) return t('localBrowser.previewStale')
  if (!s.task_id) return t('localBrowser.waiting')
  if (s.action) return browserActionLabel(t, s.action)
  return t(preview.value ? 'localBrowser.running' : 'localBrowser.previewLoading')
})
const statusDetail = computed(() => [
  statusLabel.value,
  status.value.action ? t('localBrowser.elapsedSeconds', { seconds: Math.floor((status.value.action_elapsed_ms || 0) / 1000) }) : '',
  preview.value && !previewStale.value ? t('localBrowser.previewLive') : '',
].filter(Boolean).join(' · '))

const { supported: pipSupported, target: pipTarget, opening: pipOpening, open: openPiP, close: closePiP } = useDocumentPictureInPicture(computed(() => status.value.selected), () => t('localBrowser.preview'))
const previewElement = ref<HTMLElement | null>(null)
const { positionStyle, dragging, startDrag, moveDrag, stopDrag } = useFloatingPreviewDrag(computed(() => pipTarget.value ? null : previewElement.value))
const pipFailed = ref(false)
async function togglePictureInPicture() {
  pipFailed.value = false
  if (pipTarget.value) { closePiP(); window.focus(); return }
  try { await openPiP() }
  catch { if (alive) pipFailed.value = true }
}
function openBrowserSettings() {
  closePiP()
  window.focus()
  void router.push(toolboxLocation('browserconnection'))
}

let lastFrame = 0
const endpoint = `/api/v1/sessions/${encodeURIComponent(props.sessionId)}/local-browser`
const controller = new AbortController()
let alive = true, cancelTimer: (() => void) | undefined, revision = 0
async function act(action: string) {
  if (busy.value) return
  busy.value = true; error.value = ''; revision++
  try {
    const result = await post<{ data: typeof status.value }>(endpoint, { action }, { timeout: 45000, signal: controller.signal })
    if (alive) { status.value = result.data; if (action === 'stop') preview.value = '' }
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) busy.value = false }
}
let polling = false
async function poll() {
  if (polling || !alive) return
  polling = true
  try {
    if (!busy.value && (!document.hidden || pipTarget.value)) {
      const current = revision
      const result = await get<{ data: typeof status.value }>(endpoint, { signal: controller.signal })
      if (!alive || current !== revision) return
      status.value = result.data
      if (!status.value.connected || !status.value.task_id) preview.value = ''
      if (alive && !busy.value && status.value.selected && status.value.connected && status.value.task_id && (!status.value.idle || !preview.value)) {
        const image = await post<{ data: { image_base64: string; format: string; captured_at: string } }>(endpoint, { action: 'preview' }, { timeout: 10000, signal: controller.signal })
        if (alive && revision === current && image.data.image_base64 && ['png', 'jpeg'].includes(image.data.format)) { preview.value = `data:image/${image.data.format};base64,${image.data.image_base64}`; lastFrame = Date.parse(image.data.captured_at) || Date.now(); previewStale.value = false }
      }
    }
  } catch { /* Background polling does not replace explicit action errors. */ }
  finally {
    polling = false
    if (alive) {
      previewStale.value = !!preview.value && Date.now() - lastFrame > 5000
      // Schedule in the visible PiP document when the conversation is in the background.
      const timerWindow = pipTarget.value?.ownerDocument.defaultView || window
      const timer = timerWindow.setTimeout(poll, !status.value.enabled ? 30000 : status.value.selected && !status.value.idle ? 1000 : 5000)
      cancelTimer = () => timerWindow.clearTimeout(timer)
    }
  }
}
const onVisible = () => { cancelTimer?.(); if (!document.hidden || pipTarget.value) void poll() }
watch(pipTarget, onVisible, { flush: 'post' })
onMounted(() => { document.addEventListener('visibilitychange', onVisible); void poll() })
onBeforeUnmount(() => { alive = false; document.removeEventListener('visibilitychange', onVisible); revision++; cancelTimer?.(); controller.abort(); preview.value = '' })
</script>
<style scoped>
.browser-task-preview {
  position: absolute; right: 20px; bottom: 16px; width: 320px; max-width: calc(100% - 40px);
  max-height: calc(100% - 24px); box-sizing: border-box; z-index: 5; overflow: auto;
  background: var(--td-bg-color-container); border: 1px solid var(--td-component-stroke);
  border-radius: 12px; box-shadow: 0 8px 28px #18252014, 0 2px 6px #18252008;
}
.preview-heading {
  display: flex; align-items: center; gap: 8px; min-height: 38px; padding: 0 9px 0 12px;
  cursor: grab; touch-action: none; user-select: none; position: sticky; top: 0; z-index: 1;
  background: var(--td-bg-color-container);
}
.is-dragging .preview-heading { cursor: grabbing; }
.preview-browser-icon { flex-shrink: 0; color: var(--td-text-color-secondary); }
.preview-heading strong {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-size: var(--app-text-sm); font-weight: 500; color: var(--td-text-color-secondary);
}
.preview-image {
  width: 100%; height: auto; border: 0; border-block: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary);
  display: grid; place-items: center; cursor: pointer; padding: 0; font-size: var(--app-text-xs);
}
.preview-image:disabled { cursor: default; }
.preview-image.is-empty { aspect-ratio: 16 / 9; }
.preview-image img { display: block; width: 100%; height: auto; min-height: 0; }
.preview-image:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }
.preview-empty { display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 12px; line-height: 1.5; }
.preview-empty svg { opacity: .45; }
.preview-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding: 8px 10px 8px 12px; position: sticky; bottom: 0; background: var(--td-bg-color-container); }
.preview-status {
  display: flex; align-items: center; gap: 6px; flex: 1 1 64px; min-width: 0; overflow: hidden;
  white-space: nowrap; font-size: var(--app-text-xs); line-height: 1.5; color: var(--td-text-color-secondary);
}
.preview-status i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; flex-shrink: 0; }
.preview-status > span { overflow: hidden; text-overflow: ellipsis; }
.preview-status.live i { background: var(--td-success-color); }
.preview-status.attention i { background: var(--td-warning-color); }
.preview-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; margin-left: auto; }
.preview-control, .preview-popout {
  display: inline-flex; align-items: center; justify-content: center; gap: 4px;
  min-width: 28px; min-height: 28px; padding: 4px; border: 0; border-radius: 6px;
  font: inherit; font-size: var(--app-text-xs); background: transparent;
  color: var(--td-text-color-secondary); cursor: pointer; flex-shrink: 0;
  transition: background-color .16s, color .16s;
}
.preview-control { box-sizing: border-box; padding: 4px 8px; border: 1px solid var(--td-component-stroke); font-size: var(--app-text-sm); line-height: 18px; color: var(--td-text-color-primary); }
.preview-control :deep(.t-icon) { flex-shrink: 0; color: var(--td-text-color-secondary); }
.preview-control:not(:disabled):hover, .preview-popout:not(:disabled):hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
.preview-control:focus-visible, .preview-popout:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 1px; }
.preview-control:disabled, .preview-popout:disabled { opacity: .4; cursor: default; }
.preview-error { margin: 8px 12px; color: var(--td-error-color); font-size: var(--app-text-sm); line-height: 1.5; overflow-wrap: anywhere; }
.browser-task-preview.is-pip { position: static; width: 100%; max-width: none; height: 100dvh; max-height: none; border: 0; border-radius: 0; box-shadow: none; display: flex; flex-direction: column; }
.is-pip .preview-heading { cursor: default; flex-shrink: 0; }
.is-pip .preview-image { flex: 1; min-height: 0; overflow: hidden; aspect-ratio: auto; grid-template-rows: minmax(0, 1fr); }
.is-pip .preview-image img { height: 100%; object-fit: contain; }
.is-pip .preview-toolbar { margin-top: auto; flex-shrink: 0; }
.browser-task-preview.needs-help { width: 420px; }
.preview-handoff { display: flex; flex-direction: column; align-items: stretch; gap: 8px; padding: 16px; border-top: 1px solid var(--td-component-stroke); background: var(--td-bg-color-secondarycontainer); overflow-wrap: anywhere; }
.preview-handoff strong { font-size: var(--app-text-md); font-weight: 600; color: var(--td-text-color-primary); }
.preview-handoff p { font-size: var(--app-text-sm); line-height: 1.6; margin: 0; color: var(--td-text-color-secondary); white-space: pre-wrap; }
.preview-handoff .handoff-prompt { max-height: 22vh; overflow-y: auto; color: var(--td-text-color-primary); }
.preview-handoff .handoff-locate { align-self: flex-end; flex-shrink: 0; max-width: 100%; margin-top: 2px; }
.is-pip .preview-handoff { flex-shrink: 0; }
.browser-task-preview.is-pip.needs-help { width: 100%; }
@media(max-width:720px) { .browser-task-preview { right: 12px; bottom: 12px; width: 280px; max-width: calc(100% - 24px); } .preview-toolbar { padding-left: 8px; } }
@media(max-width:480px) { .browser-task-preview.needs-help { width: calc(100% - 24px); max-width: calc(100% - 24px); } .browser-task-preview.is-pip.needs-help { width: 100%; max-width: none; } }
@media(prefers-reduced-motion: reduce) { .preview-control, .preview-popout { transition: none; } }
</style>
