<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { openMermaidFullscreen } from '@/utils/mermaidViewer'
import { renderMermaidImages } from './renderImages'

const props = defineProps<{ source: string; title: string; beforeOpen: () => Promise<void> }>()
const emit = defineEmits<{ fallback: [source: string] }>()
const { t } = useI18n()
const src = ref('')
const img = ref<HTMLImageElement>()
const controller = new AbortController()
let closeViewer: (() => void) | undefined
function failed() {
  if (!controller.signal.aborted) emit('fallback', props.source)
}
onMounted(async () => {
  try {
    const [image] = await renderMermaidImages([props.source], controller.signal)
    if (!image) return failed()
    if (!controller.signal.aborted) src.value = image
  } catch {
    failed()
  }
})
async function open() {
  await props.beforeOpen()
  if (img.value && !controller.signal.aborted) closeViewer = openMermaidFullscreen(img.value)
}
onBeforeUnmount(() => {
  controller.abort()
  closeViewer?.()
})
</script>
<template>
  <div class="mermaid-preview">
    <button v-if="src" class="open-diagram" :aria-label="t('preview.fullscreen')" @click="open">
      <img ref="img" :src="src" :alt="title" @error="failed" />
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
