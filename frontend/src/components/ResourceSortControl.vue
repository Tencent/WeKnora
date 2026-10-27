<template>
  <t-popup v-model:visible="panelVisible" trigger="click" placement="bottom-right"
    overlay-class-name="resource-sort-popup" :overlay-inner-style="{ padding: 0 }">
    <template #content>
      <div class="resource-sort-panel" role="menu" :aria-label="t('resourceSort.title')">
        <section v-for="group in groups" :key="group.key" class="resource-sort-group">
          <div class="resource-sort-group__label" :title="group.description">{{ group.label }}</div>
          <div class="resource-sort-group__options">
            <button v-for="option in group.options" :key="option.value" type="button"
              class="resource-sort-option" :class="{ active: modelValue === option.value }"
              role="menuitemradio" :aria-checked="modelValue === option.value"
              @click.stop="selectOption(option.value)">
              <span>{{ optionLabel(option) }}</span>
              <t-icon v-if="modelValue === option.value" name="check" size="14px" />
            </button>
          </div>
        </section>
      </div>
    </template>
    <button type="button" class="resource-sort-trigger" :class="{ active: panelVisible }"
      :title="`${t('resourceSort.title')}: ${activeLabel}`"
      :aria-label="`${t('resourceSort.title')}: ${activeLabel}`">
      <t-icon name="filter-sort" size="16px" />
      <span class="resource-sort-trigger__label">{{ t('resourceSort.title') }} · {{ activeLabel }}</span>
      <t-icon name="chevron-down" size="14px" class="resource-sort-trigger__caret"
        :class="{ open: panelVisible }" />
    </button>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  RESOURCE_SORT_OPTIONS,
  getResourceSortOption,
  type ResourceSortOption,
  type ResourceSortValue,
} from '@/utils/resourceSorting'

const props = defineProps<{ modelValue: ResourceSortValue }>()
const emit = defineEmits<{ 'update:modelValue': [value: ResourceSortValue] }>()
const { t } = useI18n()
const panelVisible = ref(false)

const optionLabel = (option: ResourceSortOption) => t(`resourceSort.${option.labelKey}`)

const groups = computed(() => [
  {
    key: 'updated_at',
    label: t('resourceSort.updatedTime'),
    description: t('resourceSort.updatedTimeDescription'),
    options: RESOURCE_SORT_OPTIONS.filter(option => option.sortBy === 'updated_at'),
  },
  {
    key: 'created_at',
    label: t('resourceSort.createdTime'),
    description: t('resourceSort.createdTimeDescription'),
    options: RESOURCE_SORT_OPTIONS.filter(option => option.sortBy === 'created_at'),
  },
  {
    key: 'name',
    label: t('resourceSort.name'),
    description: t('resourceSort.nameDescription'),
    options: RESOURCE_SORT_OPTIONS.filter(option => option.sortBy === 'name'),
  },
])

const activeLabel = computed(() => optionLabel(getResourceSortOption(props.modelValue)))

function selectOption(value: ResourceSortValue) {
  panelVisible.value = false
  emit('update:modelValue', value)
}
</script>

<style scoped lang="less">
.resource-sort-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  max-width: 180px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  cursor: pointer;
  transition: color 0.12s ease, background-color 0.12s ease, border-color 0.12s ease;

  &:hover,
  &.active {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &__label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__caret {
    flex-shrink: 0;
    transition: transform 0.15s ease;

    &.open {
      transform: rotate(180deg);
    }
  }
}

.resource-sort-panel {
  width: 260px;
  max-width: calc(100vw - 32px);
  padding: 4px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
}

.resource-sort-group {
  & + & {
    margin-top: 4px;
    padding-top: 4px;
    border-top: 1px solid var(--td-component-stroke);
  }

  &__label {
    padding: 6px 8px 2px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-xs);
    line-height: 18px;
  }

  &__options {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 2px;
  }
}

.resource-sort-option {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
  height: 32px;
  padding: 0 8px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}
</style>
