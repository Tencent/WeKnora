<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ source: string; title: string }>()
const emit = defineEmits<{ fallback: [source: string] }>()
const { t } = useI18n()
const dialog = ref<HTMLDialogElement>()
const frame = ref<HTMLIFrameElement>()
const frameDocument = ref('')
const ready = ref(false)
const expanded = ref(false)
const exporting = ref(false)
const exportFailed = ref(false)
let active = true
let port: MessagePort | undefined
let exportReply: MessagePort | undefined
let renderTimer: ReturnType<typeof setTimeout>
let exportTimer: ReturnType<typeof setTimeout>

function fallback() {
  if (active) emit('fallback', props.source)
}

function connect() {
  // Only the original, app-built document receives this file's private channel.
  // Its sandbox has an opaque origin, hence the wildcard target.
  if (port || !frame.value?.contentWindow) return fallback()
  const channel = new MessageChannel()
  port = channel.port1
  port.onmessage = ({ data }) => {
    if (data === 'ready') {
      ready.value = true
      clearTimeout(renderTimer)
    } else if (data === 'failed') fallback()
    else if (data === 'closed' && expanded.value) collapse()
  }
  frame.value.contentWindow.postMessage('mermaid:init', '*', [channel.port2])
  const keys = ['mermaid.zoomIn', 'mermaid.zoomOut', 'mermaid.reset', 'mermaid.close']
  port.postMessage({
    type: 'source',
    source: props.source,
    labels: Object.fromEntries(keys.map((key) => [key, t(key)])),
    dark: document.documentElement.getAttribute('theme-mode') === 'dark',
  })
}

function expand() {
  if (!ready.value || !dialog.value || expanded.value) return
  // Promote the same frame to the top layer without reparenting/reloading it.
  dialog.value.close()
  dialog.value.showModal()
  expanded.value = true
  exportFailed.value = false
  port?.postMessage({ type: 'open' })
}

function finishExport() {
  clearTimeout(exportTimer)
  exportReply?.close()
  exportReply = undefined
  exporting.value = false
}

function collapse() {
  finishExport()
  dialog.value?.close()
  dialog.value?.show()
  expanded.value = false
}

async function savePng(blob: unknown, reply: MessagePort) {
  if (
    !(blob instanceof Blob) ||
    blob.type !== 'image/png' ||
    blob.size < 33 ||
    blob.size > 32 * 1024 * 1024
  )
    throw Error('Invalid PNG')
  const signature = new Uint8Array(await blob.slice(0, 8).arrayBuffer())
  if (![137, 80, 78, 71, 13, 10, 26, 10].every((byte, i) => signature[i] === byte))
    throw Error('Invalid PNG')
  if (exportReply !== reply) return
  const link = document.createElement('a')
  link.href = URL.createObjectURL(blob)
  link.download = 'mermaid-diagram.png'
  link.click()
  setTimeout(() => URL.revokeObjectURL(link.href), 1000)
}

function exportPng() {
  if (!port || !expanded.value || exporting.value) return
  const reply = new MessageChannel()
  exportReply = reply.port1
  exporting.value = true
  exportFailed.value = false
  exportTimer = setTimeout(() => {
    finishExport()
    exportFailed.value = true
  }, 10000)
  // Only this host-side user action grants a one-shot download reply port.
  reply.port1.onmessage = async ({ data }) => {
    reply.port1.close()
    try {
      await savePng(data, reply.port1)
    } catch {
      if (exportReply === reply.port1) exportFailed.value = true
    } finally {
      if (exportReply === reply.port1) finishExport()
    }
  }
  port.postMessage({ type: 'export' }, [reply.port2])
}

onMounted(async () => {
  try {
    const { default: html } = await import('virtual:mermaid-file-frame')
    if (active) {
      renderTimer = setTimeout(fallback, 15000)
      frameDocument.value = html
    }
  } catch {
    fallback()
  }
})
onBeforeUnmount(() => {
  active = false
  clearTimeout(renderTimer)
  finishExport()
  port?.close()
})
</script>

<template>
  <dialog
    ref="dialog"
    open
    :aria-label="title"
    class="mermaid-preview"
    :class="{ expanded }"
    @cancel.prevent="port?.postMessage({ type: 'close' })"
  >
    <iframe
      v-if="frameDocument"
      ref="frame"
      :srcdoc="frameDocument"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      :title="title"
      @load="connect"
      @error="fallback"
    />
    <button
      v-if="!expanded"
      class="open-diagram"
      :disabled="!ready"
      :aria-label="t('preview.fullscreen')"
      @click="expand"
    />
    <button v-else class="export" :disabled="exporting" @click="exportPng">
      {{ t('mermaid.download') }}
    </button>
    <p v-if="expanded && exportFailed" role="alert">{{ t('common.failed') }}</p>
  </dialog>
</template>

<style scoped>
.mermaid-preview {
  position: relative;
  width: 100%;
  height: 380px;
  max-height: 60vh;
  box-sizing: border-box;
  padding: 0;
  margin: 0;
  border: 0;
  background: var(--td-bg-color-container);
}
iframe {
  width: 100%;
  height: 100%;
  display: block;
  border: 0;
}
.open-diagram {
  position: absolute;
  inset: 0;
  width: 100%;
  border: 0;
  background: transparent;
  cursor: zoom-in;
}
.expanded {
  position: fixed;
  inset: 0;
  width: 100%;
  height: 100%;
  max-width: none;
  max-height: none;
}
.export,
[role='alert'] {
  position: absolute;
  bottom: 20px;
  right: 20px;
}
[role='alert'] {
  right: auto;
  left: 20px;
}
</style>
