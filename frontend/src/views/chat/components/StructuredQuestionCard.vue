<template>
  <section ref="root" class="structured-question-card" :class="{ 'is-resolved': terminal }"
    :aria-busy="submitting" @click.stop>
    <header class="question-header">
      <div class="question-heading">
        <t-icon :name="terminal ? statusIcon : 'chat-bubble-help'" aria-hidden="true" />
        <span class="question-mode">{{ modeLabel }}</span>
      </div>
      <div class="question-progress" :aria-label="progressLabel">
        <span>{{ questionIndex }} / {{ questionTotal }}</span>
        <span class="question-remaining">{{ progressSummary }}</span>
      </div>
    </header>

    <p class="question-text">{{ question }}</p>

    <div v-if="!terminal" class="structured-question-options">
      <t-radio-group v-if="mode === 'single_choice'" v-model="selectedSingle" class="choice-group">
        <t-radio v-for="option in options" :key="option.id" :value="option.id" :disabled="locked">
          <span class="option-copy">
            <span class="option-label">{{ option.label }}</span>
            <span v-if="option.description" class="option-description">{{ option.description }}</span>
          </span>
        </t-radio>
        <t-radio v-if="allowOther" value="__other__" :disabled="locked">
          <span class="option-label">{{ t('agentStream.userInput.other') }}</span>
        </t-radio>
      </t-radio-group>

      <template v-else-if="mode === 'multiple_choice'">
        <t-checkbox-group v-model="selectedOptionIds" class="choice-group">
          <t-checkbox v-for="option in options" :key="option.id" :value="option.id" :disabled="locked">
            <span class="option-copy">
              <span class="option-label">{{ option.label }}</span>
              <span v-if="option.description" class="option-description">{{ option.description }}</span>
            </span>
          </t-checkbox>
        </t-checkbox-group>
        <t-checkbox v-if="allowOther" v-model="otherSelected" class="other-choice" :disabled="locked">
          {{ t('agentStream.userInput.other') }}
        </t-checkbox>
      </template>

      <t-input v-else-if="mode === 'short_text'" v-model="typedText" class="typed-control"
        :disabled="locked" :maxlength="validation.max_length || 500" @keydown.enter.prevent="submitAnswer(false)" />
      <t-textarea v-else-if="mode === 'long_text'" v-model="typedText" class="typed-control"
        :disabled="locked" :maxlength="validation.max_length || 5000" :autosize="{ minRows: 4, maxRows: 10 }" />
      <t-input-number v-else-if="mode === 'number'" v-model="typedNumber" class="typed-control"
        :disabled="locked" :min="validation.min_number" :max="validation.max_number" />
      <t-date-picker v-else v-model="typedDate" class="typed-control" :disabled="locked"
        format="YYYY-MM-DD" value-type="YYYY-MM-DD" />

      <t-input v-if="otherSelected" v-model="otherText" class="other-input" :disabled="locked"
        :maxlength="1000" :placeholder="t('agentStream.userInput.otherPlaceholder')"
        @keydown.enter.prevent="submitAnswer(false)" />
    </div>

    <div v-if="!terminal" class="question-actions">
      <span v-if="submitError" class="submit-error" role="alert">{{ submitError }}</span>
      <span v-else-if="localSubmitted" class="submit-status">{{ t('agentStream.userInput.continuing') }}</span>
      <span class="action-spacer" />
      <t-button v-if="allowSkip" variant="text" theme="default" :disabled="locked"
        @click="submitAnswer(true)">
        {{ t('agentStream.userInput.skip') }}
      </t-button>
      <t-button theme="primary" :loading="submitting" :disabled="locked || !answerPayload"
        @click="submitAnswer(false)">
        {{ t('agentStream.userInput.continue') }}
      </t-button>
    </div>

    <div v-else class="resolved-summary">
      <t-icon :name="statusIcon" aria-hidden="true" />
      <span>{{ resolvedSummary }}</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { resolveUserInput } from '@/api/user-input'
import {
  buildStructuredAnswer,
  structuredQuestionProgress,
  type StructuredQuestionMode,
  type StructuredQuestionOption,
  type StructuredQuestionStatus,
  type StructuredQuestionValidation,
} from '@/utils/structuredQuestion'

const props = withDefaults(defineProps<{
  pendingId: string
  question: string
  mode: StructuredQuestionMode
  fieldKey?: string
  schemaVersion?: number
  questionGroupId: string
  questionIndex: number
  questionTotal: number
  completedCount?: number
  remainingCount?: number
  options: StructuredQuestionOption[]
  allowOther?: boolean
  allowSkip?: boolean
  resolved?: boolean
  status?: StructuredQuestionStatus
  selectedOptions?: StructuredQuestionOption[]
  resolvedOtherText?: string
  resolvedValue?: unknown
  validation?: StructuredQuestionValidation
  reason?: string
}>(), {
  allowOther: true,
  allowSkip: true,
  resolved: false,
  selectedOptions: () => [],
  resolvedOtherText: '',
  reason: '',
  validation: () => ({}),
})

const emit = defineEmits<{ submitted: [pendingId: string] }>()
const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const selectedSingle = ref('')
const selectedOptionIds = ref<string[]>([])
const otherSelected = ref(false)
const otherText = ref('')
const typedText = ref(typeof props.resolvedValue === 'string' ? props.resolvedValue : '')
const typedNumber = ref<number | undefined>(typeof props.resolvedValue === 'number' ? props.resolvedValue : undefined)
const typedDate = ref(props.mode === 'date' && typeof props.resolvedValue === 'string' ? props.resolvedValue : '')
const submitting = ref(false)
const localSubmitted = ref(false)
const submitError = ref('')

watch(selectedSingle, (value) => {
  otherSelected.value = value === '__other__'
  selectedOptionIds.value = value && value !== '__other__' ? [value] : []
})

const terminal = computed(() => props.resolved || Boolean(props.status))
const locked = computed(() => submitting.value || localSubmitted.value || terminal.value)
const progress = computed(() => structuredQuestionProgress({
  question_index: props.questionIndex,
  question_total: props.questionTotal,
  completed_count: props.completedCount,
  remaining_count: props.remainingCount,
}, props.status === 'answered' || props.status === 'skipped'))
const modeLabel = computed(() => t(`agentStream.userInput.${props.mode}`))
const progressLabel = computed(() => t('agentStream.userInput.progress', {
  current: props.questionIndex, total: props.questionTotal,
}))
const progressSummary = computed(() => t('agentStream.userInput.dynamicProgress', {
  completed: progress.value.completed, remaining: progress.value.remaining,
}))
const answerPayload = computed(() => buildStructuredAnswer({
  mode: props.mode,
  fieldKey: props.fieldKey,
  schemaVersion: props.schemaVersion,
  selectedOptionIds: selectedOptionIds.value,
  otherSelected: otherSelected.value,
  otherText: otherText.value,
  value: props.mode === 'number' ? typedNumber.value : props.mode === 'date' ? typedDate.value : typedText.value,
  validation: props.validation,
  skipped: false,
  allowOther: props.allowOther,
  allowSkip: props.allowSkip,
}))

const statusIcon = computed(() => {
  if (props.status === 'answered') return 'check-circle'
  if (props.status === 'skipped') return 'minus-circle'
  return 'time'
})

const resolvedSummary = computed(() => {
  if (props.status === 'skipped') return t('agentStream.userInput.skipped')
  if (props.status === 'timed_out') return t('agentStream.userInput.timedOut')
  if (props.status === 'canceled') return t('agentStream.userInput.canceled')
  const values = props.selectedOptions.map(option => option.label)
  if (props.resolvedOtherText) values.push(props.resolvedOtherText)
  if (props.resolvedValue !== undefined && props.resolvedValue !== null) {
    values.push(Array.isArray(props.resolvedValue) ? props.resolvedValue.join('、') : String(props.resolvedValue))
  }
  return values.length
    ? t('agentStream.userInput.answeredWith', { answer: values.join('、') })
    : t('agentStream.userInput.answered')
})

async function submitAnswer(skip: boolean) {
  if (locked.value || (skip && !props.allowSkip)) return
  const payload = skip ? buildStructuredAnswer({
    mode: props.mode, fieldKey: props.fieldKey, schemaVersion: props.schemaVersion,
    selectedOptionIds: [], otherSelected: false, otherText: '', value: undefined,
    validation: props.validation, skipped: true, allowOther: props.allowOther, allowSkip: props.allowSkip,
  }) : answerPayload.value
  if (!payload) return
  submitting.value = true
  submitError.value = ''
  try {
    await resolveUserInput(props.pendingId, payload)
    localSubmitted.value = true
    emit('submitted', props.pendingId)
  } catch (error: any) {
    submitError.value = error?.response?.data?.error?.message || error?.message || t('agentStream.userInput.submitFailed')
  } finally {
    submitting.value = false
  }
}

function focusFirstOption() {
  const target = root.value?.querySelector<HTMLElement>('input, textarea')
  target?.focus()
}

onMounted(() => nextTick(focusFirstOption))
</script>

<style scoped lang="less">
@import './structured-question-card.less';
</style>
