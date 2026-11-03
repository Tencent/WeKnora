<template>
  <!-- On the right edge, where the panel opens, clear of the upload progress
       card in the bottom-right corner. -->
  <AssistantButton v-if="assistant.entry === 'launcher' && !assistant.visible" class="assistant-launcher"
    data-guide="assistant-entry" :label="name" :icon-size="18" @click="assistant.open()" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import AssistantButton from './AssistantButton.vue'

const { t } = useI18n()
const assistant = useAssistantStore()
const name = computed(() => assistant.displayName || t('assistant.title'))
</script>

<style lang="less" scoped>
.assistant-launcher {
  position: fixed;
  top: 50%;
  right: 0;
  z-index: 1400;
  justify-content: flex-start;
  gap: 6px;
  max-width: 36px;
  height: 36px;
  padding: 0 8px;
  box-sizing: border-box;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-right: 0;
  border-radius: var(--app-radius-lg) 0 0 var(--app-radius-lg);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  transform: translateY(-50%);
  transition: max-width var(--app-motion-base) ease, box-shadow var(--app-motion-fast) ease,
    background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover, &:focus-visible {
    max-width: 200px;
    box-shadow: var(--td-shadow-2);
  }
  :deep(.assistant-button__label) {
    font-size: var(--app-text-sm);
  }
}
</style>
