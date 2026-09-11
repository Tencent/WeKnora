<template>
  <aside v-if="status.selected" ref="previewElement" class="browser-task-preview" :class="{ 'is-dragging': dragging }" :style="positionStyle" :aria-label="t('localBrowser.preview')">
    <div class="preview-heading" @pointerdown="startDrag" @pointermove="moveDrag" @pointerup="stopDrag" @pointercancel="stopDrag" @lostpointercapture="stopDrag"><BrowserIcon class="preview-browser-icon" width="20" height="20" /><strong>{{ t('localBrowser.local') }}</strong><span>{{ t(!status.connected ? 'localBrowser.offline' : status.paused ? 'localBrowser.paused' : status.needs_help ? 'localBrowser.needHelp' : status.task_id ? 'localBrowser.connected' : 'localBrowser.waiting') }}</span></div>
    <button class="preview-image" :disabled="!status.connected || !status.task_id || busy" :aria-label="t('localBrowser.locateWindow')" @click="act('focus')">
      <img v-if="preview" :src="preview" :alt="t('localBrowser.preview')" />
      <span v-else>{{ t(status.connected ? 'localBrowser.waiting' : 'localBrowser.reconnectShort') }}</span>
      <span v-if="status.connected && status.task_id" class="locate"><t-icon name="jump" size="13px" />{{ t('localBrowser.locateWindow') }}</span>
    </button>
    <p class="preview-sync" role="status">{{ t(!status.connected ? 'localBrowser.reconnectShort' : status.idle ? 'localBrowser.previewIdle' : previewStale ? 'localBrowser.previewStale' : preview ? 'localBrowser.previewLive' : 'localBrowser.previewLoading') }}</p>
    <p v-if="status.needs_help" class="preview-help"><span v-if="status.help_prompt">{{ status.help_prompt }}<br /></span>{{ t('localBrowser.helpHint') }}</p>
    <p v-if="error" class="preview-error" role="alert">{{ error }}</p>
    <p class="preview-scope">{{ t('localBrowser.controlScope') }}</p>
    <div class="preview-actions">
      <t-button v-if="status.connected" :title="t(status.paused ? 'localBrowser.resume' : 'localBrowser.pauseHint')" size="small" theme="default" variant="text" :disabled="busy" @click="act(status.paused ? 'resume' : 'pause')">{{ t(status.paused ? 'localBrowser.resume' : 'localBrowser.pause') }}</t-button>
      <t-button v-else size="small" theme="default" variant="text" @click="uiStore.openSettings('browserconnection')">{{ t('localBrowser.settingsTitle') }}</t-button>
      <t-button :title="t('localBrowser.stopHint')" size="small" theme="default" variant="text" :disabled="busy" @click="act('stop')">{{ t('localBrowser.stop') }}</t-button>
    </div>
  </aside>
</template>
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { get, post } from '@/utils/request'
import { useUIStore } from '@/stores/ui'
import BrowserIcon from '@/components/icons/BrowserIcon.vue'
import { useFloatingPreviewDrag } from '@/composables/useFloatingPreviewDrag'
const previewElement = ref<HTMLElement | null>(null)
const { positionStyle, dragging, startDrag, moveDrag, stopDrag } = useFloatingPreviewDrag(previewElement)
const props = defineProps<{ sessionId: string }>()
const { t } = useI18n(), uiStore = useUIStore()
const status = ref({ enabled: false, selected: false, connected: false, paused: false, idle: false, needs_help: false, help_prompt: '', task_id: '' })
const busy = ref(false), error = ref(''), preview = ref(''), previewStale = ref(false)
let lastFrame = 0
const endpoint = `/api/v1/sessions/${encodeURIComponent(props.sessionId)}/local-browser`
const controller = new AbortController()
let alive = true, timer: ReturnType<typeof setTimeout> | undefined, revision = 0
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
    if (!busy.value && !document.hidden) {
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
  finally { polling = false; if (alive) { previewStale.value = !!preview.value && Date.now() - lastFrame > 5000; timer = setTimeout(poll, !status.value.enabled ? 30000 : status.value.selected && !status.value.idle ? 1000 : 5000) } }
}
const onVisible = () => { if (!document.hidden) { clearTimeout(timer); void poll() } }
onMounted(() => { document.addEventListener('visibilitychange', onVisible); void poll() })
onBeforeUnmount(() => { alive = false; document.removeEventListener('visibilitychange', onVisible); revision++; clearTimeout(timer); controller.abort(); preview.value = '' })
</script>
<style scoped>
.browser-task-preview { position: absolute; right: 20px; bottom: 16px; width: 240px; max-width: calc(100% - 40px); max-height: calc(100% - 24px); box-sizing: border-box; z-index: 5; overflow: auto; background: var(--td-bg-color-container); border: 1px solid var(--td-component-border); border-radius: 12px; box-shadow: 0 4px 20px #0000000d; }
.preview-browser-icon { flex-shrink: 0; color: var(--td-text-color-secondary); }
.preview-heading { display: flex; align-items: center; gap: 7px; padding: 10px 12px; cursor: grab; touch-action: none; user-select: none; position: sticky; top: 0; z-index: 1; background: var(--td-bg-color-container); }.is-dragging .preview-heading { cursor: grabbing; }.preview-heading strong { font-size: 12px; font-weight: 500; flex: 1; }.preview-heading span { color: var(--td-text-color-secondary); font-size: 11px; }
.preview-image { width: 100%; height: 126px; border: 0; border-block: 1px solid var(--td-component-border); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); display: grid; place-items: center; position: relative; cursor: pointer; padding: 0; font-size: 12px; }.preview-image:disabled { cursor: default; }.preview-image img { width: 100%; height: 100%; object-fit: contain; object-position: top; }.preview-image:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }.locate { position: absolute; display: inline-flex; align-items: center; gap: 5px; bottom: 8px; padding: 4px 8px; border-radius: 5px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); box-shadow: 0 1px 5px #00000014; }.preview-image:hover .locate { color: var(--td-brand-color); }
.preview-sync, .preview-help, .preview-scope { font-size: 11px; line-height: 1.5; margin: 6px 12px; color: var(--td-text-color-secondary); }
.preview-help { color: var(--td-brand-color); }
.preview-actions { display: flex; flex-wrap: wrap; gap: 4px; justify-content: space-between; padding: 5px 8px; }.preview-error { margin: 8px 12px; color: var(--td-error-color); font-size: 12px; line-height: 1.5; }
@media(max-width:720px){.browser-task-preview { right: 12px; bottom: 12px; width: 200px; }.preview-image { height: 100px; }}
</style>
