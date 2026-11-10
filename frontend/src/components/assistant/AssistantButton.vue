<template>
  <button type="button" class="assistant-button" :class="{ 'is-icon-only': iconOnly, 'is-active': active }"
    :disabled="disabled" :aria-label="label" :title="iconOnly ? label : undefined" data-assistant-ignore>
    <ResourceIcon type="assistant" :size="iconSize ?? (iconOnly ? 16 : 14)" />
    <span v-if="!iconOnly" class="assistant-button__label">{{ label }}</span>
  </button>
</template>

<script setup lang="ts">
import ResourceIcon from '@/components/icons/ResourceIcon.vue'

defineProps<{
  label: string
  iconOnly?: boolean
  iconSize?: number
  active?: boolean
  disabled?: boolean
}>()
</script>

<style lang="less" scoped>
.assistant-button {
  --wails-draggable: no-drag;
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 4px;
  box-sizing: border-box;
  height: 26px;
  padding: 0 6px;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &.is-icon-only {
    width: 28px;
    height: 28px;
    padding: 0;
  }

  &.is-active,
  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-brand-color);
  }

  &:active:not(:disabled) {
    background: var(--td-bg-color-container-active);
    color: var(--td-brand-color);
  }

  &:disabled {
    color: var(--td-text-color-disabled);
    cursor: not-allowed;
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }
}
</style>
