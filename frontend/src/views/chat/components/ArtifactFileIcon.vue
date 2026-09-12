<template>
  <span class="artifact-file-icon" aria-hidden="true" v-html="iconSvg" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { renderArtifactFileIcon } from '@/utils/artifactFileIcon'

const props = defineProps<{ fileName: string }>()
const iconSvg = computed(() => renderArtifactFileIcon(props.fileName))
</script>

<style scoped lang="less">
@import '@/components/css/artifact-file-icon.less';

.artifact-file-icon {
  display: inline-flex;
  flex-shrink: 0;
  width: 32px;
  height: 38px;

  :deep(.artifact-file-icon-svg) {
    .artifact-file-icon-style();
    width: 100%;
    height: 100%;
  }
}
</style>
