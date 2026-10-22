<template>
  <!-- On the right edge, where the panel opens, clear of the upload progress
       card in the bottom-right corner. -->
  <button v-if="assistant.entry === 'launcher' && !assistant.visible" type="button" class="assistant-launcher"
    :aria-label="name" @click="assistant.open()">
    <t-icon name="compass" class="assistant-launcher__icon" />
    <span class="assistant-launcher__label">{{ name }}</span>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'

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
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 36px;
  height: 36px;
  padding: 0 10px;
  box-sizing: border-box;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-right: 0;
  border-radius: var(--app-radius-lg) 0 0 var(--app-radius-lg);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  color: var(--td-brand-color);
  font: inherit;
  cursor: pointer;
  transform: translateY(-50%);
  transition: max-width var(--app-motion-base) ease, box-shadow var(--app-motion-fast) ease;

  &:hover, &:focus-visible {
    max-width: 200px;
    box-shadow: var(--td-shadow-2);
  }
}

.assistant-launcher__icon {
  flex-shrink: 0;
  font-size: var(--app-text-lg);
}

.assistant-launcher__label {
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
  white-space: nowrap;
}
</style>
