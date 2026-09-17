<template>
  <t-popup
    v-model:visible="visible"
    trigger="click"
    placement="top"
    :show-arrow="true"
    destroy-on-close
    overlay-class-name="context-usage-popup"
    :overlay-inner-style="{ padding: 0 }"
  >
    <button
      type="button"
      class="context-usage-ring"
      :aria-label="$t('input.contextUsage.title')"
      :title="$t('input.contextUsage.title')"
    >
      <svg viewBox="0 0 18 18" aria-hidden="true">
        <circle class="context-usage-ring__track" cx="9" cy="9" r="7" />
        <circle
          class="context-usage-ring__fill"
          cx="9"
          cy="9"
          r="7"
          :stroke="ringColor"
          :stroke-dasharray="circumference"
          :stroke-dashoffset="dashOffset"
        />
      </svg>
    </button>
    <template #content>
      <div class="context-usage-card" @click.stop>
        <div class="context-usage-card__header">
          <span class="context-usage-card__title">{{ $t('input.contextUsage.title') }}</span>
          <button type="button" class="context-usage-card__close" :aria-label="$t('common.close')" @click="visible = false">
            ×
          </button>
        </div>
        <template v-if="hasData">
          <div class="context-usage-card__summary">
            <span class="context-usage-card__percent">{{ percentLabel }}</span>
            <span class="context-usage-card__used">{{
              $t('input.contextUsage.used', { used: formatContextUsageCount(total), window: formatContextUsageCount(windowSize) })
            }}</span>
          </div>
          <div class="context-usage-card__bar" aria-hidden="true">
            <span
              v-for="segment in barSegments"
              :key="segment.key"
              class="context-usage-card__bar-seg"
              :style="{ width: segment.percent + '%', background: segment.color }"
            />
          </div>
          <ul class="context-usage-card__rows">
            <li v-for="row in CONTEXT_USAGE_CATEGORIES" :key="row.key" class="context-usage-card__row">
              <span class="context-usage-card__dot" :style="{ background: row.color }" />
              <span class="context-usage-card__label">{{ $t('input.contextUsage.categories.' + row.key) }}</span>
              <span class="context-usage-card__value">~{{ formatContextUsageCount(categoryTokens(row.key)) }}</span>
            </li>
          </ul>
        </template>
        <p v-else class="context-usage-card__empty">{{ $t('input.contextUsage.empty') }}</p>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  CONTEXT_USAGE_CATEGORIES,
  contextUsageBarSegments,
  contextUsageCategoryTokens,
  contextUsagePercent,
  formatContextUsageCount,
  type ContextUsage,
  type ContextUsageCategoryKey,
} from '@/utils/contextUsage'

const props = defineProps<{
  usage?: ContextUsage | null
}>()

const visible = ref(false)
const radius = 7
const circumference = 2 * Math.PI * radius

const total = computed(() => props.usage?.total ?? 0)
const windowSize = computed(() => props.usage?.window ?? 0)
const percent = computed(() => contextUsagePercent(props.usage))
const percentLabel = computed(() => `${percent.value.toFixed(1)}%`)
const hasData = computed(() => windowSize.value > 0 || total.value > 0)
const dashOffset = computed(() => circumference * (1 - percent.value / 100))
const ringColor = computed(() => {
  if (percent.value >= 85) return '#ef4444'
  if (percent.value >= 60) return '#f59e0b'
  return '#22c55e'
})
const barSegments = computed(() => contextUsageBarSegments(props.usage))

function categoryTokens(key: ContextUsageCategoryKey): number {
  return contextUsageCategoryTokens(props.usage, key)
}
</script>

<style lang="less">
.context-usage-popup {
  .t-popup__content {
    padding: 0;
    border-radius: 12px;
    box-shadow: var(--td-shadow-2);
  }
}
</style>

<style scoped lang="less">
.context-usage-ring {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
  flex-shrink: 0;

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover, #e6e6e6);
  }

  svg {
    width: 18px;
    height: 18px;
    transform: rotate(-90deg);
  }

  circle {
    fill: none;
    stroke-width: 1.8;
  }
}

.context-usage-ring__track {
  stroke: var(--td-component-stroke, #e7e7e7);
}

.context-usage-ring__fill {
  stroke-linecap: round;
  transition: stroke-dashoffset 0.2s ease, stroke 0.2s ease;
}

.context-usage-card {
  width: 280px;
  padding: 12px 14px 10px;
  color: var(--td-text-color-primary);
}

.context-usage-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.context-usage-card__title {
  font-size: 13px;
  font-weight: 600;
}

.context-usage-card__close {
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  padding: 0 2px;
}

.context-usage-card__summary {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 8px;
}

.context-usage-card__percent {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.context-usage-card__used {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.context-usage-card__bar {
  display: flex;
  height: 6px;
  border-radius: 999px;
  overflow: hidden;
  background: var(--td-bg-color-secondarycontainer, #f2f2f2);
  margin-bottom: 10px;
}

.context-usage-card__bar-seg {
  display: block;
  height: 100%;
  min-width: 0;
  flex: 0 0 auto;
}

.context-usage-card__rows {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.context-usage-card__row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.context-usage-card__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.context-usage-card__label {
  flex: 1;
  color: var(--td-text-color-primary);
}

.context-usage-card__value {
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

.context-usage-card__empty {
  margin: 4px 0 2px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}
</style>
