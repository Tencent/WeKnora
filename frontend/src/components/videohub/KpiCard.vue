<template>
  <t-tooltip :content="tooltip" placement="top">
    <article class="kpi-card" tabindex="0">
      <div class="kpi-card__title"><span>{{ title }}</span><t-icon name="info-circle" size="14px" /></div>
      <div class="kpi-card__body"><strong>{{ formattedValue }}</strong><small v-if="unit">{{ unit }}</small></div>
      <div v-if="trend" class="kpi-card__trend" :class="trend.value >= 0 ? 'is-up' : 'is-down'">
        <t-icon :name="trend.value >= 0 ? 'chevron-up' : 'chevron-down'" size="14px" />
        <span>{{ Math.abs(trend.value) }}{{ trend.suffix ?? '%' }} 较上周期</span>
      </div>
    </article>
  </t-tooltip>
</template>

<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ title: string; value: number; unit?: string; trend?: { value: number; suffix?: string }; tooltip: string }>()
const formattedValue = computed(() => Number.isInteger(props.value) ? props.value.toLocaleString('zh-CN') : props.value.toFixed(1))
</script>

<style scoped>
.kpi-card { position: relative; display: grid; gap: var(--td-comp-margin-s); min-height: 132px; padding: calc(var(--td-comp-margin-s) * 2.5); border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); outline: none; background: var(--td-bg-color-container); transition: border-color .15s ease, box-shadow .15s ease; }
.kpi-card:hover, .kpi-card:focus-visible { border-color: var(--td-brand-color); box-shadow: var(--td-shadow-1); }
.kpi-card__title { display: flex; align-items: center; justify-content: space-between; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.kpi-card__body { display: flex; align-items: baseline; gap: calc(var(--td-comp-margin-s) / 2); }
.kpi-card__body strong { color: var(--td-text-color-primary); font-size: 28px; font-weight: 600; line-height: 1.2; }
.kpi-card__body small { color: var(--td-text-color-secondary); }
.kpi-card__trend { display: flex; align-items: center; gap: calc(var(--td-comp-margin-s) / 4); font-size: var(--td-font-size-body-small); }
.kpi-card__trend.is-up { color: var(--td-success-color); }
.kpi-card__trend.is-down { color: var(--td-error-color); }
</style>
