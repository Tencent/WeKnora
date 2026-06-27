<template>
  <t-dropdown :options="options" trigger="click" placement="bottom-left" attach="body" :max-column-width="240"
    :max-height="280" @click="onSelect">
    <button type="button" class="integrations-agent-filter" :class="{ 'integrations-agent-filter--active': modelValue }"
      :aria-label="ariaLabel">
      <t-icon name="filter" size="14px" class="integrations-agent-filter__icon" />
      <span v-if="selectedAgentName" class="integrations-agent-filter__name">{{ selectedAgentName }}</span>
      <t-icon name="chevron-down" size="12px" class="integrations-agent-filter__chevron" />
    </button>
  </t-dropdown>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CustomAgent } from '@/api/agent'

const props = defineProps<{
  modelValue: string
  agents: CustomAgent[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const { t } = useI18n()

const selectedAgentName = computed(() => {
  if (!props.modelValue) return ''
  return props.agents.find((item) => item.id === props.modelValue)?.name || ''
})

const ariaLabel = computed(() =>
  selectedAgentName.value
    ? t('integrations.filterByAgentWithName', { name: selectedAgentName.value })
    : t('integrations.filterByAgent'),
)

const options = computed(() => [
  { content: t('integrations.filterAllAgents'), value: '' },
  ...props.agents.map((agent) => ({
    content: agent.name,
    value: agent.id,
    active: agent.id === props.modelValue,
  })),
])

function onSelect(data: { value?: string }) {
  emit('update:modelValue', data?.value || '')
}
</script>

<style scoped lang="less">
.integrations-agent-filter {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 220px;
  padding: 2px 6px 2px 4px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background 0.2s ease, color 0.2s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-secondary);
  }

  &--active {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      color: var(--td-brand-color);
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  &__icon {
    flex-shrink: 0;
  }

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    font-weight: 500;
    color: inherit;
  }

  &__chevron {
    flex-shrink: 0;
    opacity: 0.7;
  }
}
</style>
