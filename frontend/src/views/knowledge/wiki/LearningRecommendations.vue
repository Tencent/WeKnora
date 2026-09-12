<template>
  <section class="learning-recommendations">
    <h3>{{ t('learning.recommendations') }}</h3>
    <p v-if="!items.length" class="muted">{{ t('learning.noRecommendations') }}</p>
    <ul v-else>
      <li v-for="item in items" :key="item.page_id">
        <button class="topic-link" type="button" @click="$emit('open-page', item.slug)">{{ item.title }}</button>
        <div class="meta">
          <LearningStateBadge :state="item.mastery.state" :mastery="item.mastery" />
          <LearningStateBadge v-if="item.familiar" familiar />
        </div>
        <div class="reasons">
          <span v-for="reason in item.reason_codes" :key="reason">{{ te(`learning.reasons.${reason}`) ? t(`learning.reasons.${reason}`) : reason }}</span>
        </div>
      </li>
    </ul>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LearningRecommendation } from '@/api/learning'
import LearningStateBadge from './LearningStateBadge.vue'
defineProps<{ items: LearningRecommendation[] }>()
defineEmits<{ 'open-page': [slug: string] }>()
const { t, te } = useI18n()
</script>
<style scoped>
h3 { font-size: 13px; margin: 0 0 8px; font-weight: 600; }
ul { list-style: none; padding: 0; margin: 0; }
li { padding: 9px 0; border-bottom: 1px solid var(--td-component-stroke); }
li:last-child { border: 0; }
.topic-link { border: 0; background: none; padding: 0; color: var(--td-text-color-primary); text-align: left; cursor: pointer; font: inherit; overflow-wrap: anywhere; }
.topic-link:hover { color: var(--td-brand-color); }
.meta { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 4px; }
.reasons { display: flex; flex-wrap: wrap; gap: 4px 10px; margin-top: 3px; color: var(--td-text-color-secondary); font-size: 12px; overflow-wrap: anywhere; }
.muted { color: var(--td-text-color-secondary); font-size: 12px; }
</style>
