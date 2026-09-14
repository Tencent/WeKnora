<template>
  <div class="answer-result" role="status">
    <strong :class="result.correct ? 'answer-correct' : 'answer-incorrect'">{{ t(result.correct ? 'learning.correct' : 'learning.incorrect') }}</strong>
    <p>{{ result.explanation }}</p>
    <div v-for="(evidence, index) in result.evidence" :key="`${evidence.chunk_id}-${index}`" class="evidence">
      <blockquote>{{ evidence.quote }}</blockquote>
      <t-button size="small" variant="text" @click="$emit('open-source', evidence.knowledge_id)">
        <template #icon><t-icon name="file" /></template>{{ t('learning.source', { index: index + 1 }) }}
      </t-button>
    </div>
    <LearningStateBadge :state="result.mastery.state" :mastery="result.mastery" />
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LearningAnswerResult } from '@/api/learning'
import LearningStateBadge from './LearningStateBadge.vue'
defineProps<{ result: LearningAnswerResult }>()
defineEmits<{ 'open-source': [knowledgeId: string] }>()
const { t } = useI18n()
</script>
<style scoped>
.answer-result { font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }
p { white-space: pre-wrap; margin: 6px 0; }
.answer-correct { color: var(--td-success-color); }
.answer-incorrect { color: var(--td-warning-color); }
.evidence { margin: 10px 0; }
blockquote { margin: 0; padding: 0 10px; border-left: 2px solid var(--td-component-border); color: var(--td-text-color-secondary); white-space: pre-wrap; }
</style>
