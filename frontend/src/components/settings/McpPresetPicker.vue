<template>
  <section class="setting-drawer__section preset-picker" :aria-label="t('mcpServiceDialog.presets.title')">
    <h4 class="setting-drawer__section-title">{{ t('mcpServiceDialog.presets.title') }}</h4>
    <p class="form-desc">{{ t('mcpServiceDialog.presets.hint') }}</p>
    <t-input v-model="query" :placeholder="t('mcpServiceDialog.presets.search')"
      :aria-label="t('mcpServiceDialog.presets.search')" clearable />
    <div class="preset-picker__list">
      <button v-for="preset in filtered" :key="preset.id" type="button"
        class="preset-picker__card" :class="{ 'is-selected': selectedId === preset.id }"
        :aria-pressed="selectedId === preset.id" @click="emit('select', preset)">
        <img :src="aceIcon" alt="" width="24" height="24" />
        <span class="preset-picker__text">
          <strong>{{ preset.name }}</strong>
          <span>{{ t(preset.labelKey) }}</span>
        </span>
        <t-icon v-if="selectedId === preset.id" name="check" aria-hidden="true" />
      </button>
    </div>
    <p v-if="filtered.length === 0" role="status" class="form-desc">{{ t('common.noResult') }}</p>
    <button type="button" class="preset-picker__custom" @click="emit('custom')">
      {{ t('mcpServiceDialog.presets.custom') }}
    </button>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import aceIcon from '@/assets/img/acedatacloud.png'
import { filterMcpPresets, type McpServicePreset } from '@/utils/mcpServicePresets'

defineProps<{ selectedId?: string }>()
const emit = defineEmits<{ select: [preset: McpServicePreset]; custom: [] }>()
const { t } = useI18n()
const query = ref('')
const filtered = computed(() => filterMcpPresets(query.value))
</script>

<style scoped lang="less">
.preset-picker { display: grid; gap: 10px; }
.preset-picker__list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.preset-picker__card {
  display: flex; align-items: flex-start; gap: 8px; padding: 12px;
  text-align: left; color: var(--td-text-color-primary); background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-sm); cursor: pointer;
  &:hover, &.is-selected { border-color: var(--td-brand-color); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
}
.preset-picker__text {
  display: flex; flex: 1; flex-direction: column; gap: 4px; min-width: 0;
  strong { font-size: var(--app-text-sm); }
  span { font-size: var(--app-text-xs); color: var(--td-text-color-secondary); }
}
.preset-picker__custom {
  justify-self: start; padding: 0; background: none; border: 0;
  color: var(--td-brand-color); font: inherit; cursor: pointer;
}
@media (max-width: 600px) { .preset-picker__list { grid-template-columns: 1fr; } }
</style>
