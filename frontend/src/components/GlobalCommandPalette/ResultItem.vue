<template>
  <button
    type="button"
    :class="['cmdk-item', { 'cmdk-item--selected': selected }]"
    :data-cmdk-index="index"
    @click="$emit('primary')"
    @mousemove="onHover"
  >
    <div class="cmdk-item__icon">
      <slot name="icon">
        <t-icon :name="iconName" size="14px" />
      </slot>
    </div>
    <div class="cmdk-item__body">
      <div class="cmdk-item__title">
        <slot name="title">
          <span v-html="title" />
        </slot>
        <span v-if="badge" :class="['cmdk-item__badge', `cmdk-item__badge--${badgeVariant || 'default'}`]">
          {{ badge }}
        </span>
        <span v-if="score != null" class="cmdk-item__score">{{ (score * 100).toFixed(0) }}%</span>
      </div>
      <div v-if="$slots.subtitle || subtitle" class="cmdk-item__subtitle">
        <slot name="subtitle">
          <span v-html="subtitle" />
        </slot>
      </div>
    </div>
    <div class="cmdk-item__actions">
      <slot name="actions" />
      <span v-if="shortcut" class="cmdk-item__shortcut">
        <kbd>⌘</kbd><kbd>{{ shortcut }}</kbd>
      </span>
    </div>
  </button>
</template>

<script setup lang="ts">
/**
 * A single result row inside the command palette.
 * Host component owns selection state; this one just mirrors it via the
 * `selected` prop and emits hover/primary events.
 */
defineProps<{
  index: number
  selected?: boolean
  iconName?: string
  title?: string
  subtitle?: string
  badge?: string
  badgeVariant?: 'vector' | 'keyword' | 'default'
  score?: number
  /** Visible hint for the ⌘N shortcut that triggers this row (N: 1-9). */
  shortcut?: number | string
}>()

const emit = defineEmits<{
  (e: 'primary'): void
  (e: 'hover', index: number): void
}>()

const onHover = (e: MouseEvent) => {
  const idx = Number((e.currentTarget as HTMLElement).dataset.cmdkIndex)
  if (!Number.isNaN(idx)) emit('hover', idx)
}
</script>

<style lang="less" scoped>
.cmdk-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  width: 100%;
  padding: 8px 12px;
  border-radius: 8px;
  border: none;
  background: transparent;
  cursor: pointer;
  text-align: left;
  font: inherit;
  color: var(--td-text-color-primary);
  transition: background 0.1s ease;
}

.cmdk-item--selected {
  background: var(--td-bg-color-secondarycontainer);
}

.cmdk-item__icon {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  margin-top: 2px;
}

.cmdk-item__body {
  flex: 1;
  min-width: 0;
}

.cmdk-item__title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
  line-height: 20px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.cmdk-item__badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  font-weight: 500;
  flex-shrink: 0;
  line-height: 1.4;

  &--vector {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
  }

  &--keyword {
    background: rgba(255, 152, 0, 0.1);
    color: var(--td-warning-color);
  }

  &--default {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }
}

.cmdk-item__score {
  margin-left: auto;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.cmdk-item__subtitle {
  margin-top: 2px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  text-overflow: ellipsis;
  word-break: break-word;
}

.cmdk-item__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 4px;
}

.cmdk-item__shortcut {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  opacity: 0.55;
  transition: opacity 0.1s;

  kbd {
    display: inline-block;
    padding: 0 4px;
    min-width: 14px;
    font-family: inherit;
    line-height: 14px;
    text-align: center;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: 3px;
    color: var(--td-text-color-secondary);
  }
}

.cmdk-item--selected .cmdk-item__shortcut {
  opacity: 1;
}

:deep(.search-highlight) {
  background: rgba(255, 213, 0, 0.35);
  color: inherit;
  padding: 0 1px;
  border-radius: 2px;
}
</style>
