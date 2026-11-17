<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import mermaidUrl from 'mermaid/dist/mermaid.min.js?url&no-inline'
import { frameDocument } from './frameDocument'
import bootstrap from './render.js?raw'
import { mermaidConfig } from '@/utils/mermaidConfig'
import { openMermaidFullscreen } from '@/utils/mermaidViewer'

const props = defineProps<{ source: string; title: string; beforeOpen: () => Promise<void> }>()
const emit = defineEmits<{ fallback: [source: string] }>()
const { t } = useI18n()
const frame = ref<HTMLIFrameElement>()
const html = ref('')
const src = ref('')
const img = ref<HTMLImageElement>()
const controller = new AbortController()
let port: MessagePort | undefined
let closeViewer: (() => void) | undefined
let timer: ReturnType<typeof setTimeout>
function failed() {
  if (!controller.signal.aborted) emit('fallback', props.source)
}
onMounted(async () => {
  try {
    timer = setTimeout(failed, 15000)
    const response = await fetch(mermaidUrl, { signal: controller.signal })
    if (!response.ok) throw Error('Mermaid unavailable')
    html.value = frameDocument(await response.text(), bootstrap)
  } catch {
    failed()
  }
})
function loaded() {
  clearTimeout(timer)
}
function connect() {
  if (!frame.value?.contentWindow || port) return failed()
  const ch = new MessageChannel()
  port = ch.port1
  port.onmessage = ({ data }) => {
    if (typeof data !== 'string') return failed()
    src.value = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(data)
  }
  frame.value.contentWindow.postMessage(
    {
      source: props.source,
      config: mermaidConfig(document.documentElement.getAttribute('theme-mode') === 'dark'),
    },
    '*',
    [ch.port2],
  )
}
async function open() {
  await props.beforeOpen()
  if (img.value && !controller.signal.aborted) closeViewer = openMermaidFullscreen(img.value)
}
onBeforeUnmount(() => {
  controller.abort()
  clearTimeout(timer)
  port?.close()
  closeViewer?.()
})
</script>
<template>
  <div class="mermaid-preview">
    <iframe
      v-if="html && !src"
      ref="frame"
      :srcdoc="html"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      aria-hidden="true"
      tabindex="-1"
      @load="connect"
    />
    <button v-if="src" class="open-diagram" :aria-label="t('preview.fullscreen')" @click="open">
      <img ref="img" :src="src" :alt="title" @load="loaded" @error="failed" />
    </button>
  </div>
</template>
<style scoped>
.mermaid-preview {
  position: relative;
  display: grid;
  place-items: center;
  background: var(--td-bg-color-container);
  height: min(380px, 60vh);
}
iframe {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  border: 0;
  visibility: hidden;
}
button {
  padding: 0;
  border: 0;
  background: transparent;
  cursor: zoom-in;
}
img {
  display: block;
  max-width: 100%;
  max-height: min(380px, 60vh);
}
</style>
