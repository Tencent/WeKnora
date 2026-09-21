<template>
  <span v-if="sourceType === 'manual' || sourceType === 'url'" class="document-source-icon"
    :class="`document-source-icon--${sourceType}`" aria-hidden="true">
    <svg viewBox="0 0 32 38" fill="none">
      <path class="file-sheet" d="M6 1.5h13L28.5 11v22A3.5 3.5 0 0 1 25 36.5H6A3.5 3.5 0 0 1 2.5 33V5A3.5 3.5 0 0 1 6 1.5Z" />
      <path class="file-fold" d="M19 1.5V8a3 3 0 0 0 3 3h6.5" />
      <path class="file-lines" d="M8 14h9M8 18h14" />
    </svg>
    <span class="document-source-icon__badge"><t-icon :name="sourceType === 'manual' ? 'edit' : 'link'" size="13px" /></span>
  </span>
  <ArtifactFileIcon v-else :file-name="fileName" />
</template>

<script setup lang="ts">
import ArtifactFileIcon from '@/views/chat/components/ArtifactFileIcon.vue';
defineProps<{ sourceType?: string; fileName: string }>();
</script>

<style scoped lang="less">
@import '@/components/css/artifact-file-icon.less';
.document-source-icon {
  position: relative;
  display: inline-flex;
  flex: 0 0 32px;
  width: 32px;
  height: 38px;
  color: #7885ad;
  &--url { color: #53978e; }
  > svg { .artifact-file-icon-style(); color: inherit; }
  &__badge {
    position: absolute;
    right: 1px;
    bottom: 1px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 16px;
    border-radius: var(--app-radius-xs);
    background: currentColor;
    .t-icon { color: #fff; }
  }
}
</style>
