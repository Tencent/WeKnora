<template>
  <div class="setting-card" :class="{ 'setting-card--disabled': disabled }">
    <div class="setting-card__header">
      <h3 class="setting-card__title" :title="title">{{ title }}</h3>
      <div class="setting-card__header-right">
        <slot name="controls" />
        <t-dropdown
          v-if="actions && actions.length > 0"
          :options="actions"
          placement="bottom-right"
          attach="body"
          @click="(data: any) => emit('action', data.value)"
        >
          <t-button variant="text" shape="square" size="small" class="setting-card__more">
            <t-icon name="more" />
          </t-button>
        </t-dropdown>
      </div>
    </div>
    <div v-if="$slots.tags" class="setting-card__tags">
      <slot name="tags" />
    </div>
    <p v-if="description" class="setting-card__desc">{{ description }}</p>
    <div v-if="$slots.meta" class="setting-card__meta">
      <slot name="meta" />
    </div>
  </div>
</template>

<script setup lang="ts">
interface DropdownOption {
  content: string
  value: string
  theme?: 'default' | 'success' | 'warning' | 'error' | 'primary'
}

interface Props {
  title: string
  description?: string
  disabled?: boolean
  actions?: DropdownOption[]
}

withDefaults(defineProps<Props>(), {
  description: '',
  disabled: false,
  actions: () => []
})

const emit = defineEmits<{
  (e: 'action', value: string): void
}>()
</script>

<style lang="less" scoped>
.setting-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
  }

  &--disabled {
    background: var(--td-bg-color-secondarycontainer);

    .setting-card__title {
      color: var(--td-text-color-secondary);
    }

    &:hover {
      border-color: var(--td-brand-color-light);
      box-shadow: none;
    }
  }
}

.setting-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.setting-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.setting-card__header-right {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.setting-card__more {
  color: var(--td-text-color-placeholder);
  padding: 4px;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.setting-card__tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-height: 20px;
}

.setting-card__desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-all;
}

.setting-card__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
</style>
