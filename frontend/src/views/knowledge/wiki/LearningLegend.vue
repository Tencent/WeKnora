<template>
  <div class="learning-legend" :aria-label="$t('learning.masteryLegend')">
    <LearningStateBadge v-for="state in learningStates" :key="state" :state="state" />
  </div>
</template>
<script setup lang="ts">
import { learningStates } from '@/composables/learningHelpers'
import LearningStateBadge from './LearningStateBadge.vue'
</script>
<style scoped>
.learning-legend { display: flex; flex-wrap: wrap; gap: 6px 12px; padding: 8px 0; }
</style>
