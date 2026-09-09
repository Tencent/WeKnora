<template>
  <svg class="artifact-file-icon" :class="`kind-${kind}`" viewBox="0 0 32 38" fill="none" aria-hidden="true">
    <path class="file-sheet" d="M6 1.5h13L28.5 11v22A3.5 3.5 0 0 1 25 36.5H6A3.5 3.5 0 0 1 2.5 33V5A3.5 3.5 0 0 1 6 1.5Z" />
    <path class="file-fold" d="M19 1.5V8a3 3 0 0 0 3 3h6.5" />
    <path class="file-lines" d="M8 14h9M8 18h14" />
    <rect x="5" y="23" width="24" height="11" rx="3" fill="currentColor" />
    <text x="17" y="30.8" text-anchor="middle">{{ label }}</text>
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getFileIcon } from '@/utils/files'
import { resolveFilePreviewExt } from '@/utils/filePreview'

const props = defineProps<{ fileName: string }>()
const kind = computed(() => getFileIcon(props.fileName))
const label = computed(() => {
  const ext = resolveFilePreviewExt(props.fileName)
  return /^[a-z0-9]{1,4}$/i.test(ext) ? ext.toUpperCase() : 'FILE'
})
</script>

<style scoped lang="less">
.artifact-file-icon {
  flex-shrink: 0;
  width: 32px;
  height: 38px;
  color: var(--td-text-color-secondary);

  &.kind-file-word { color: #5381c4; }
  &.kind-file-excel { color: #419781; }
  &.kind-file-powerpoint { color: #c18a4e; }
  &.kind-file-pdf { color: #c56868; }
  &.kind-image,
  &.kind-video { color: #9273bd; }
  &.kind-code,
  &.kind-sound { color: #5d95ad; }

  .file-sheet {
    fill: var(--td-bg-color-container);
    stroke: currentColor;
    stroke-opacity: 0.35;
    stroke-width: 1.2;
  }

  .file-fold,
  .file-lines {
    stroke: currentColor;
    stroke-opacity: 0.35;
    stroke-width: 1.2;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  text {
    fill: #fff;
    font-family: ui-sans-serif, system-ui, sans-serif;
    font-size: 7px;
    font-weight: 650;
    letter-spacing: 0.2px;
  }
}
</style>
