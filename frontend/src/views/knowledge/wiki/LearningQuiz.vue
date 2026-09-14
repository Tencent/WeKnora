<template>
  <section class="learning-quiz" :aria-label="t('learning.practice')" :aria-busy="state.quizLoading || !!state.submitting">
    <div class="quiz-heading">
      <h3>{{ state.quiz?.title || title || t('learning.practice') }}</h3>
      <t-button v-if="state.quiz?.slug" size="small" variant="text" @click="$emit('open-page', state.quiz.slug)">{{ t('learning.openPage') }}</t-button>
    </div>
    <div v-if="state.quizLoading" class="quiz-status" role="status">
      <t-loading size="small" />
      <span>{{ t(state.quiz?.status === 'running' ? 'learning.running' : 'learning.pending') }}</span>
      <t-button size="small" variant="text" @click="controller.cancelQuiz()">{{ t('learning.cancelPolling') }}</t-button>
    </div>
    <div v-if="state.paused" class="quiz-status" role="status">
      <span>{{ t('learning.paused') }}</span>
      <t-button size="small" variant="outline" @click="controller.retryQuiz()">{{ t('learning.checkAgain') }}</t-button>
    </div>
    <div v-if="state.quizError || state.answerError" class="quiz-status error" role="alert">
      <span>{{ t(`learning.errors.${state.quizError || state.answerError}`) }}</span>
      <t-button v-if="!state.submitting" size="small" variant="text" @click="controller.retryQuiz()">{{ t('learning.refresh') }}</t-button>
    </div>
    <div v-if="state.quiz?.status === 'failed' || state.quiz?.status === 'stale'" class="quiz-status" role="status">
      <span>{{ t(state.quiz.status === 'stale' ? 'learning.sourceStale' : 'learning.failed') }}</span>
      <t-button size="small" variant="outline" :disabled="state.busy" @click="controller.loadQuiz('', true)">{{ t('learning.regenerate') }}</t-button>
    </div>
    <t-button v-if="!state.quiz && !state.quizLoading && (canPrepare || state.previousQuiz)" size="small" :disabled="state.busy || !state.settings?.enabled" @click="controller.loadQuiz('', true)">
      <template #icon><t-icon name="edit-1" /></template>{{ t('learning.startPractice') }}
    </t-button>
    <ol v-if="state.quiz?.status === 'ready'" class="questions">
      <li v-for="(question, index) in state.quiz.questions" :key="question.id">
        <fieldset :disabled="question.answered || !!state.submitting || state.busy || state.quizLoading">
          <legend>{{ index + 1 }}. {{ question.prompt }}</legend>
          <label v-for="option in question.options" :key="option.id" class="quiz-option"
            :class="{ selected: selected(question.id) === option.id, correct: question.answered && question.result?.correct_option === option.id }">
            <input type="radio" :name="`${instanceId}-${question.id}`" :value="option.id"
              :checked="selected(question.id) === option.id" :disabled="!!state.attempts[question.id]"
              @change="selections[question.id] = option.id" />
            <span>{{ option.text }}</span>
            <t-icon v-if="question.answered && question.result?.correct_option === option.id" name="check-circle" :aria-label="t('learning.correctAnswer')" />
          </label>
        </fieldset>
        <t-button v-if="!question.answered" size="small" :loading="state.submitting === question.id"
          :disabled="!selected(question.id) || !!state.submitting || state.quizLoading || state.busy"
          @click="controller.submit(question.id, selected(question.id))">{{ state.attempts[question.id] ? t('learning.retryAnswer') : t('learning.submit') }}</t-button>
        <LearningFeedback v-else-if="question.result" :result="question.result" @open-source="$emit('open-source', $event)" />
        <span v-else>{{ t('learning.answered') }}</span>
      </li>
    </ol>
    <t-button v-if="completed" size="small" variant="outline" :disabled="state.busy || state.quizLoading" @click="controller.loadQuiz('', true)">{{ t('learning.newPractice') }}</t-button>
    <details v-if="state.previousQuiz" class="previous-quiz">
      <summary>{{ t('learning.previousPractice') }}</summary>
      <ol class="questions">
        <li v-for="question in state.previousQuiz.questions" :key="question.id">
          <p>{{ question.prompt }}</p>
          <p>{{ t('learning.yourAnswer') }}: {{ question.options.find(option => option.id === question.result?.selected_option)?.text }}</p>
          <p>{{ t('learning.correctAnswer') }}: {{ question.options.find(option => option.id === question.result?.correct_option)?.text }}</p>
          <LearningFeedback v-if="question.answered && question.result" :result="question.result" @open-source="$emit('open-source', $event)" />
        </li>
      </ol>
    </details>
    <p v-if="state.quiz?.status === 'ready' && !state.quiz.questions.length" class="quiz-status">{{ t('learning.noQuestions') }}</p>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LearningController } from '@/composables/useLearningState'
import LearningFeedback from './LearningFeedback.vue'
const props = defineProps<{ controller: LearningController; title?: string; canPrepare?: boolean }>()
defineEmits<{ 'open-page': [slug: string]; 'open-source': [knowledgeId: string] }>()
const { t } = useI18n()
const instanceId = useId()
const state = computed(() => props.controller.state)
const completed = computed(() => state.value.quiz?.status === 'ready' && state.value.quiz.questions.length > 0 && state.value.quiz.questions.every(question => question.answered))
const selections = ref<Record<string, string>>({})
watch(() => state.value.quiz?.id, () => { selections.value = {} }, { flush: 'sync' })
function selected(id: string) {
  const question = state.value.quiz?.questions.find(item => item.id === id)
  return (question?.answered ? question.result?.selected_option : '') || state.value.attempts[id]?.option_id || selections.value[id] || ''
}
</script>
<style scoped>
.learning-quiz { min-width: 0; overflow-wrap: anywhere; }
.quiz-heading, .quiz-status { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.quiz-heading { justify-content: space-between; margin-bottom: 10px; }
h3 { font-size: 14px; font-weight: 600; margin: 0; }
.quiz-status { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 10px; }
.error { color: var(--td-error-color); }
.questions { list-style: none; margin: 0; padding: 0; }
.questions > li { padding: 14px 0; border-bottom: 1px solid var(--td-component-stroke); }
.questions > li:first-child { padding-top: 0; }
.questions > li:last-child { border: 0; }
fieldset { margin: 0 0 10px; padding: 0; border: 0; min-width: 0; }
legend { font-weight: 500; line-height: 1.6; margin-bottom: 8px; padding: 0; width: 100%; }
.quiz-option { display: flex; align-items: flex-start; gap: 8px; padding: 8px; margin-top: 5px; border: 1px solid var(--td-component-stroke); border-radius: 4px; line-height: 1.5; }
.quiz-option input { flex: none; margin: 4px 0 0; accent-color: var(--td-brand-color); }
.quiz-option span { flex: 1; min-width: 0; }
.quiz-option.selected { background: var(--td-bg-color-secondarycontainer); }
.quiz-option.correct { border-color: var(--td-success-color); }
.previous-quiz { margin-top: 16px; font-size: 13px; }
.previous-quiz summary { cursor: pointer; color: var(--td-text-color-secondary); }
</style>
