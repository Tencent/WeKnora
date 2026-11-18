<script setup lang="ts">
import { onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import DocumentPreview from './document-preview.vue'
import { RESOURCE_PREVIEW_EVENT, type LoadedProtectedFile } from '@/utils/protectedResource'

const file = shallowRef<LoadedProtectedFile | null>(null)
const visible = ref(false)
const version = ref(0)
function releaseFile() {
  if (file.value) URL.revokeObjectURL(file.value.blobURL)
  file.value = null
}
function open(event: Event) {
  const detail = (event as CustomEvent<LoadedProtectedFile>).detail
  if (!detail || !(detail.blob instanceof Blob) || !detail.blobURL?.startsWith('blob:')) return
  event.preventDefault()
  // The originating message can unmount while this drawer remains open.
  // Own the download URL independently of the message's shared cache.
  releaseFile()
  file.value = { ...detail, blobURL: URL.createObjectURL(detail.blob) }
  version.value++
  visible.value = true
}
onMounted(() => window.addEventListener(RESOURCE_PREVIEW_EVENT, open))
watch(visible, (value) => { if (!value) releaseFile() })
onUnmounted(() => {
  window.removeEventListener(RESOURCE_PREVIEW_EVENT, open)
  releaseFile()
})
</script>

<template>
  <t-drawer v-model:visible="visible" :header="file?.fileName" size="70%"
    :z-index="2200" attach="body" :footer="false" destroy-on-close>
    <template v-if="file">
      <a :href="file.blobURL" :download="file.fileName">{{ $t('agent.artifactDrawer.download') }}</a>
      <DocumentPreview :key="version" :source-blob="file.blob" :file-name="file.fileName"
        :file-type="file.blob.type" :active="visible" />
    </template>
  </t-drawer>
</template>

<style>
.protected-resource-card {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  padding: 14px 18px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-lg);
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  overflow-wrap: anywhere;
  text-decoration: none;
}
.protected-resource-card::before { content: '↗'; margin-right: 12px; }
.protected-resource-card:hover { border-color: var(--td-brand-color); }
</style>
