<template>
  <t-popup
    v-model:visible="visible"
    trigger="click"
    placement="bottom-left"
    :overlay-style="{ padding: 0 }"
    :overlay-inner-style="{ padding: 0 }"
  >
    <template #content>
      <div class="session-group-card">
        <div class="session-group-header">{{ headerLabel }}</div>
        <div class="session-group-list">
          <button
            v-for="item in modes"
            :key="item.value"
            type="button"
            class="session-group-row"
            :class="{ active: item.value === current }"
            @click="handleSelect(item.value)"
          >
            <span class="session-group-row-name">{{ item.label }}</span>
            <t-icon
              v-if="item.value === current"
              name="check"
              class="session-group-row-check"
              size="14px"
            />
          </button>
        </div>
      </div>
    </template>
    <slot />
  </t-popup>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { SessionGroupMode } from './sessionGrouping'

interface GroupModeItem {
  value: SessionGroupMode
  label: string
}

defineProps<{
  modes: GroupModeItem[]
  current: SessionGroupMode
  headerLabel: string
}>()

const emit = defineEmits<{
  (e: 'select', value: SessionGroupMode): void
}>()

const visible = ref(false)

const handleSelect = (value: SessionGroupMode): void => {
  visible.value = false
  emit('select', value)
}
</script>

<style scoped lang="less">
.session-group-card {
  min-width: 180px;
  max-width: 240px;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}

.session-group-header {
  padding: 8px 12px 6px;
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  border-bottom: 1px solid var(--td-component-border);
  user-select: none;
}

.session-group-list {
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.session-group-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-size: 13px;
  line-height: 1.4;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
  text-align: left;
  width: 100%;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.active {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.session-group-row-name {
  flex: 1 1 auto;
}

.session-group-row-check {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}
</style>
