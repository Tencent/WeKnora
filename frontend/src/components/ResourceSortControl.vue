<template>
  <t-popup v-model:visible="panelVisible" trigger="click" placement="bottom-right"
    overlay-class-name="resource-sort-popup" :overlay-inner-style="{ padding: 0 }">
    <template #content>
      <div class="resource-sort-panel" role="menu" :aria-label="t('resourceSort.title')">
        <section v-for="group in groups" :key="group.key" class="resource-sort-group">
          <div class="resource-sort-group__heading">
            <div class="resource-sort-group__label">{{ group.label }}</div>
            <div class="resource-sort-group__description">{{ group.description }}</div>
          </div>
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
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 12px;
  cursor: pointer;
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--td-bg-color-container) 72%, transparent);
  transition: color 0.12s ease, background-color 0.12s ease, border-color 0.12s ease;

  &:hover,
  &.active {
    color: var(--td-brand-color);
    border-color: color-mix(in srgb, var(--td-brand-color) 38%, var(--td-component-stroke));
    background: var(--td-brand-color-light);
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
  width: 330px;
  padding: 6px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
}

.resource-sort-group {
  padding: 7px 6px 8px;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }

  &__heading {
    padding: 0 4px 6px;
  }

  &__label {
    font-size: 13px;
    line-height: 20px;
    font-weight: 600;
  }

  &__description {
    margin-top: 1px;
    color: var(--td-text-color-secondary);
    font-size: 11px;
    line-height: 17px;
  }

  &__options {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 4px;
  }
}

.resource-sort-option {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
  height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}
</style>
