<template>
  <div class="cmdk-group" v-if="$slots.default">
    <div class="cmdk-group__header">
      <span class="cmdk-group__label">{{ label }}</span>
      <span v-if="count != null" class="cmdk-group__count">({{ count }})</span>
      <span v-if="action" class="cmdk-group__spacer" />
      <button
        v-if="action"
        type="button"
        class="cmdk-group__action"
        @click="$emit('action')"
      >
        {{ action }}
      </button>
    </div>
    <div class="cmdk-group__body">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  label: string
  count?: number
  action?: string
}>()

defineEmits<{
  (e: 'action'): void
}>()
</script>

<style lang="less" scoped>
.cmdk-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 0;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
    margin-top: 4px;
    padding-top: 8px;
  }
}

.cmdk-group__header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 12px 2px;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.3px;
  color: var(--td-text-color-placeholder);
  text-transform: uppercase;
}

.cmdk-group__count {
  color: var(--td-text-color-disabled);
  font-weight: 400;
}

.cmdk-group__spacer {
  flex: 1;
}

.cmdk-group__action {
  border: none;
  background: transparent;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 11px;
  color: var(--td-brand-color);
  cursor: pointer;
  text-transform: none;
  letter-spacing: normal;

  &:hover {
    background: rgba(7, 192, 95, 0.08);
  }
}

.cmdk-group__body {
  display: flex;
  flex-direction: column;
}
</style>
