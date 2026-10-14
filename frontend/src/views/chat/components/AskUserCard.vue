<template>
  <div class="action-card interaction-inline" :class="{ 'action-pending': !resolved }">
    <div class="action-header no-results">
      <div class="action-title">
        <t-icon class="action-title-icon" :name="headerIcon" />
        <span class="action-name">{{ $t('agentStream.askUser.title') }}</span>
      </div>
    </div>

    <div class="ask-question">{{ question.question }}</div>

    <div v-if="!resolved" class="ask-body" @click.stop>
      <t-checkbox-group v-if="options.length && question.multi_select" v-model="selectedMany" :disabled="locked"
        class="ask-options">
        <t-checkbox v-for="option in options" :key="option" :value="option">{{ option }}</t-checkbox>
      </t-checkbox-group>
      <t-radio-group v-else-if="options.length" v-model="selectedOne" :disabled="locked" class="ask-options">
        <t-radio v-for="option in options" :key="option" :value="option">{{ option }}</t-radio>
      </t-radio-group>

      <div v-for="field in fields" :key="field.name" class="ask-field">
        <label class="ask-field-label">
          {{ field.label || field.name }}<span v-if="field.required" class="ask-required">*</span>
        </label>
        <t-input-number v-if="field.type === 'number'" v-model="values[field.name]" :disabled="locked"
          theme="normal" size="small" class="ask-number" />
        <t-select v-else-if="field.type === 'select' || field.type === 'multiselect'" v-model="values[field.name]"
          :multiple="field.type === 'multiselect'" :disabled="locked" size="small" clearable
          :placeholder="$t('agentStream.askUser.selectPlaceholder')">
          <t-option v-for="option in field.options || []" :key="option" :value="option" :label="option" />
        </t-select>
        <t-input v-else v-model="values[field.name]" :disabled="locked" size="small" :maxlength="maxTextLen" />
      </div>

      <t-textarea v-if="acceptsText" v-model="text" class="ask-text" :disabled="locked" :maxlength="maxTextLen"
        :autosize="{ minRows: 1, maxRows: 6 }"
        :placeholder="freeTextOnly ? $t('agentStream.askUser.textPlaceholder') : $t('agentStream.askUser.otherPlaceholder')" />
    </div>

    <div class="interaction-status-summary">
      <div class="results-summary-text">
        <span v-if="resolved || submitted" class="status-label">{{ statusLine }}</span>
        <span v-else class="inline-actions">
          <button type="button" class="inline-action" :disabled="submitting" @click.stop="submit(true)">
            {{ $t('agentStream.askUser.decline') }}
          </button>
          <span class="inline-dot">·</span>
          <button type="button" class="inline-action is-primary" :disabled="submitting || !canSubmit"
            @click.stop="submit(false)">
            {{ $t('agentStream.askUser.submit') }}
          </button>
        </span>
        <template v-if="!resolved && !submitted && !parked && secondsLeft >= 0">
          <span class="inline-dot">·</span>
          <span class="action-timer" :class="timerClass">{{ countdownText }}</span>
        </template>
        <template v-if="!resolved && !submitted && parked">
          <span class="inline-dot">·</span>
          <span class="action-timer">{{ parkedText }}</span>
        </template>
      </div>
    </div>

    <div v-if="answerSummary" class="ask-answer">{{ answerSummary }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveAgentPrompt } from '@/api/mcp-service'
import { resolveEmbedAgentPrompt } from '@/api/embed'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { buildAskUserResponse, canSubmitAskUser, summarizeAskUserAnswer, type AskUserQuestion } from '@/utils/askUser'

const props = defineProps<{
  pendingId: string
  request?: AskUserQuestion
  timeoutSeconds?: number
  hotWaitSeconds?: number
  suspendable?: boolean
  suspended?: boolean
  expiresAt?: string
  requestedAt?: number
  resolved?: boolean
  answered?: boolean
  response?: Record<string, unknown>
  resolveReason?: string
  timedOut?: boolean
  canceled?: boolean
  abandoned?: boolean
  embeddedMode?: boolean
  embedChannelId?: string
  embedToken?: string
  embedSessionId?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>()

const { t } = useI18n()
const resumeSuspendedRun = inject(resumeSuspendedRunKey, undefined)
const { parked, secondsLeft, timerClass, countdownText, parkedText } = usePromptCountdown(props)

const maxTextLen = 4000
const question = computed<AskUserQuestion>(() => props.request || { question: '' })
const options = computed(() => question.value.options || [])
const fields = computed(() => question.value.fields || [])
const freeTextOnly = computed(() => !options.value.length && !fields.value.length)
const acceptsText = computed(() => freeTextOnly.value || Boolean(question.value.allow_free_text))

const selectedOne = ref('')
const selectedMany = ref<string[]>([])
const values = reactive<Record<string, unknown>>({})
const text = ref('')
const submitting = ref(false)
const submitted = ref<Record<string, unknown> | null>(null)
const locked = computed(() => submitting.value || Boolean(submitted.value))

const draft = () => buildAskUserResponse(question.value, {
  selected: question.value.multi_select ? selectedMany.value : (selectedOne.value ? [selectedOne.value] : []),
  values,
  text: text.value,
})
const canSubmit = computed(() => canSubmitAskUser(question.value, draft()))

const headerIcon = computed(() => {
  if (!props.resolved) return 'help-circle'
  return props.answered ? 'check-circle' : 'minus-circle'
})

const statusLine = computed(() => {
  if (!props.resolved) {
    if (submitted.value?.declined) return t('agentStream.askUser.declinedTag')
    return t('agentStream.askUser.submitted')
  }
  if (props.abandoned) return t('agentStream.toolApproval.abandonedTag')
  if (props.answered) return t('agentStream.askUser.answeredTag')
  if (props.timedOut) return t('agentStream.askUser.timedOutTag')
  if (props.canceled) return t('agentStream.askUser.canceledTag')
  if (props.resolveReason?.includes('declined')) return t('agentStream.askUser.declinedTag')
  return t('agentStream.askUser.unansweredTag')
})

const answerSummary = computed(() => {
  const answer = props.resolved ? props.response : submitted.value
  if (!answer || answer.declined) return ''
  return summarizeAskUserAnswer(question.value, answer)
})

const embedTarget = () =>
  props.embeddedMode && props.embedChannelId && props.embedToken && props.embedSessionId
  && props.embedSessionSig && props.embedVisitorId

const submit = async (decline: boolean) => {
  if (props.resolved || submitting.value || submitted.value) return
  const response = decline ? { declined: true } : draft()
  submitting.value = true
  try {
    const { resumed } = await (embedTarget()
      ? resolveEmbedAgentPrompt(
        props.embedChannelId!, props.embedToken!, props.embedSessionId!, props.embedSessionSig!,
        props.embedVisitorId!, props.pendingId, 'ask_user', response,
      )
      : resolveAgentPrompt(props.pendingId, 'ask_user', response))
    submitted.value = response
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.error?.message || e?.message
      || t('agentStream.askUser.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';

// The question reads like the agent's other step text; the card's controls
// stay compact so the timeline keeps its rhythm.
.ask-question {
  padding: 2px 0 0;
  font-size: var(--agent-step-text-size, 14px);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.ask-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 6px 0 2px;
  max-width: 520px;
}

.ask-options {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 16px;

  :deep(.t-radio),
  :deep(.t-checkbox) {
    margin-right: 0;
    font-size: var(--agent-step-summary-size, 13px);
  }

  :deep(.t-radio__label),
  :deep(.t-checkbox__label) {
    color: var(--td-text-color-secondary);
  }
}

.ask-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ask-field-label {
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.ask-number {
  width: 100%;
}

.ask-text {
  :deep(.t-textarea__inner) {
    font-size: var(--agent-step-summary-size, 13px);
  }

  // The limit is far above a typical answer; the counter is noise here.
  :deep(.t-textarea__info_wrapper),
  :deep(.t-textarea__limit) {
    display: none;
  }
}

.ask-answer {
  padding: 2px 0 0;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.ask-required {
  margin-left: 2px;
  color: var(--td-error-color);
}
</style>
