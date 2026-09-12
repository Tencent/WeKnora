<template>
  <t-tooltip :content="tooltip">
    <span class="learning-badge" :class="{ familiar }">
      <span class="learning-dot" :style="{ background: familiar ? 'transparent' : learningColors[state || 'unseen'] }"></span>
      {{ familiar ? t('learning.familiar') : t(`learning.states.${state || 'unseen'}`) }}
      <t-icon v-if="mastery?.source_stale" name="error-circle" />
    </span>
  </t-tooltip>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LearningMasteryView } from '@/api/learning'
import { learningColors } from '@/composables/learningHelpers'
const props = defineProps<{ state?: LearningMasteryView['state']; mastery?: LearningMasteryView; familiar?: boolean }>()
const { t } = useI18n()
const tooltip = computed(() => props.familiar ? t('learning.familiarTooltip') : props.mastery
  ? t('learning.masteryTooltip', { count: props.mastery.attempts, probability: Math.round(props.mastery.p_mastery * 100) }) + (props.mastery.source_stale ? ` ${t('learning.sourceStale')}` : '')
  : t('learning.masteryLegend'))
</script>
<style scoped>
.learning-badge { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; line-height: 20px; color: var(--td-text-color-secondary); }
.learning-dot { width: 7px; height: 7px; border-radius: 2px; flex: none; }
.familiar .learning-dot { border: 2px solid #0052d9; border-radius: 50%; box-sizing: border-box; width: 10px; height: 10px; }
</style>
