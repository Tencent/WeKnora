<template>
  <div v-if="interactive" class="prompt-panel" @click.stop>
    <div class="prompt-question">{{ question.question }}</div>

    <div v-if="options.length" class="prompt-options" role="listbox" :aria-multiselectable="multiSelect">
      <button v-for="(option, index) in options" :key="option" type="button" role="option" class="prompt-option"
        :class="{ 'is-selected': isSelected(option) }" :aria-selected="isSelected(option)" :disabled="submitting"
        @click="toggleOption(option)">
        <span class="prompt-option-key">{{ optionKey(index) }}</span>
        <span class="prompt-option-label">{{ option }}</span>
        <t-icon v-if="isSelected(option)" class="prompt-option-check" name="check" />
      </button>
    </div>

    <div v-for="field in fields" :key="field.name" class="ask-field">
      <label :id="`${pendingId}-${field.name}-label`" class="ask-field-label">
        {{ field.label || field.name }}<span v-if="field.required" class="ask-required">*</span>
      </label>
      <t-input-number v-if="field.type === 'number'" v-model="values[field.name]" :disabled="submitting"
        theme="normal" size="small" class="ask-number" />
      <div v-else-if="field.type === 'multiselect'" class="ask-field-options" role="group"
        :aria-labelledby="`${pendingId}-${field.name}-label`">
        <label v-for="option in field.options || []" :key="option" class="ask-field-option"
          :class="{ 'is-selected': fieldHasOption(field.name, option), 'is-disabled': submitting }">
          <input type="checkbox" :checked="fieldHasOption(field.name, option)" :disabled="submitting"
            @change="toggleFieldOption(field.name, option)" />
          <span class="ask-field-option-mark" aria-hidden="true"><CheckIcon size="12px" /></span>
          <span class="ask-field-option-text">{{ option }}</span>
        </label>
      </div>
      <t-select v-else-if="field.type === 'select'" v-model="values[field.name]"
        :disabled="submitting" size="small" clearable
        :popup-props="{ overlayClassName: 'wk-popover' }"
        :placeholder="$t('agentStream.askUser.selectPlaceholder')">
        <t-option v-for="option in field.options || []" :key="option" :value="option" :label="option" />
      </t-select>
      <t-input v-else v-model="values[field.name]" :disabled="submitting" size="small" :maxlength="maxTextLen" />
    </div>

    <t-textarea v-if="acceptsText" v-model="text" class="ask-text" :disabled="submitting" :maxlength="maxTextLen"
      :autosize="{ minRows: 1, maxRows: 6 }"
      :placeholder="freeTextOnly ? $t('agentStream.askUser.textPlaceholder') : $t('agentStream.askUser.otherPlaceholder')" />

    <div class="prompt-footer">
      <div class="prompt-footer-meta">
        <span v-if="parked" class="action-timer">{{ parkedText }}</span>
        <span v-else-if="secondsLeft >= 0" class="action-timer" :class="timerClass">{{ countdownText }}</span>
      </div>
      <div class="prompt-footer-actions">
        <button type="button" class="prompt-btn" :disabled="submitting" @click="submit(true)">
          {{ $t('agentStream.askUser.decline') }}
        </button>
        <button type="button" class="prompt-btn is-primary" :disabled="submitting || !canSubmit" @click="submit(false)">
          {{ $t('agentStream.askUser.submit') }}<span class="prompt-btn-key" aria-hidden="true">↵</span>
        </button>
      </div>
    </div>
  </div>

  <div v-else class="action-card interaction-inline ask-inline" :class="{ 'action-pending': open }">
    <div class="action-header no-results">
      <div class="action-title">
        <ChatTimelineIcon class="action-title-icon" :name="headerIcon" :active="open" />
        <span class="action-name">{{ $t('agentStream.askUser.title') }}</span>
        <span class="ask-status" role="status">
          {{ awaitingInDock ? $t('agentStream.promptDock.waiting') : statusLine }}
        </span>
        <button v-if="awaitingInDock" type="button" class="inline-action is-primary" @click.stop="dock?.focus(pendingId)">
          {{ $t('agentStream.promptDock.handle') }}
        </button>
      </div>
    </div>

    <div class="ask-question">{{ question.question }}</div>

    <div v-if="answerSummary" class="ask-answer">
      <span class="ask-answer-label">{{ $t('agentStream.askUser.answerLabel') }}</span>
      <div class="ask-answer-text">{{ answerSummary }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import ChatTimelineIcon from '@/components/ChatTimelineIcon.vue'
import { computed, inject, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { CheckIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveAgentPrompt } from '@/api/mcp-service'
import { resolveEmbedAgentPrompt } from '@/api/embed'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { markPromptSubmitted, promptSubmission, usePromptDock } from '@/composables/usePromptDock'
import { buildAskUserResponse, canSubmitAskUser, summarizeAskUserAnswer, type AskUserQuestion } from '@/utils/askUser'
import { isSubmitKey, isTextEntry, optionIndexForKey, optionKey } from '@/utils/promptKeys'

const props = defineProps<{
  pendingId: string
  // dock: the answer form, shown above the composer. inline: the timeline row.
  variant?: 'dock' | 'inline'
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
const dock = usePromptDock()
const { parked, secondsLeft, timerClass, countdownText, parkedText } = usePromptCountdown(props)

const maxTextLen = 4000
const question = computed<AskUserQuestion>(() => props.request || { question: '' })
const options = computed(() => question.value.options || [])
const fields = computed(() => question.value.fields || [])
const multiSelect = computed(() => Boolean(question.value.multi_select))
const freeTextOnly = computed(() => !options.value.length && !fields.value.length)
const acceptsText = computed(() => freeTextOnly.value || Boolean(question.value.allow_free_text))

const submission = computed(() => promptSubmission(props.pendingId))
const open = computed(() => !props.resolved && !submission.value)
// Without a dock (a view that does not provide one) the row stays answerable.
const interactive = computed(() => open.value && (props.variant === 'dock' || !dock))
const awaitingInDock = computed(() => open.value && props.variant !== 'dock' && Boolean(dock?.isDocked(props.pendingId)))

const selectedOne = ref('')
const selectedMany = ref<string[]>([])
const values = reactive<Record<string, unknown>>({})
const text = ref('')
const submitting = ref(false)

const fieldHasOption = (name: string, option: string) =>
  Array.isArray(values[name]) && (values[name] as string[]).includes(option)

const toggleFieldOption = (name: string, option: string) => {
  if (submitting.value) return
  const selected = Array.isArray(values[name]) ? values[name] as string[] : []
  values[name] = selected.includes(option)
    ? selected.filter(value => value !== option)
    : [...selected, option]
}

const isSelected = (option: string) =>
  multiSelect.value ? selectedMany.value.includes(option) : selectedOne.value === option

const toggleOption = (option: string) => {
  if (submitting.value) return
  if (!multiSelect.value) {
    selectedOne.value = selectedOne.value === option ? '' : option
    return
  }
  selectedMany.value = selectedMany.value.includes(option)
    ? selectedMany.value.filter((o) => o !== option)
    : [...selectedMany.value, option]
}

const draft = () => buildAskUserResponse(question.value, {
  selected: multiSelect.value ? selectedMany.value : (selectedOne.value ? [selectedOne.value] : []),
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
    if (submission.value?.declined) return t('agentStream.askUser.declinedTag')
    if (submission.value) return t('agentStream.askUser.submitted')
    return t('agentStream.askUser.unansweredTag')
  }
  if (props.abandoned) return t('agentStream.toolApproval.abandonedTag')
  if (props.answered) return t('agentStream.askUser.answeredTag')
  if (props.timedOut) return t('agentStream.askUser.timedOutTag')
  if (props.canceled) return t('agentStream.askUser.canceledTag')
  if (props.resolveReason?.includes('declined')) return t('agentStream.askUser.declinedTag')
  return t('agentStream.askUser.unansweredTag')
})

const answerSummary = computed(() => {
  const answer = props.resolved ? props.response : submission.value
  if (!answer || answer.declined) return ''
  return summarizeAskUserAnswer(question.value, answer)
})

const embedTarget = () =>
  props.embeddedMode && props.embedChannelId && props.embedToken && props.embedSessionId
  && props.embedSessionSig && props.embedVisitorId

const submit = async (decline: boolean) => {
  if (!open.value || submitting.value) return
  if (!decline && !canSubmit.value) return
  const response = decline ? { declined: true } : draft()
  submitting.value = true
  try {
    const { resumed } = await (embedTarget()
      ? resolveEmbedAgentPrompt(
        props.embedChannelId!, props.embedToken!, props.embedSessionId!, props.embedSessionSig!,
        props.embedVisitorId!, props.pendingId, 'ask_user', response,
      )
      : resolveAgentPrompt(props.pendingId, 'ask_user', response))
    markPromptSubmitted(props.pendingId, response)
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.error?.message || e?.message
      || t('agentStream.askUser.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}

/** Keyboard shortcuts while docked: a letter picks an option, Enter submits. */
const handleKey = (e: KeyboardEvent): boolean => {
  if (!interactive.value) return false
  const target = e.target as HTMLElement | null
  if (isSubmitKey(e)) {
    // Enter inside a select picks from its dropdown.
    if (target?.closest('.t-select, .t-select__wrap')) return false
    void submit(false)
    return true
  }
  if (isTextEntry(target) || e.metaKey || e.ctrlKey || e.altKey) return false
  const index = optionIndexForKey(e.key)
  if (index < 0 || index >= options.value.length) return false
  toggleOption(options.value[index])
  return true
}

defineExpose({ handleKey })
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';

.ask-inline {
  min-width: 0;

  .action-title {
    column-gap: 8px;
    row-gap: 4px;
  }

  .action-name {
    font-size: var(--agent-step-summary-size, 13px);
  }
}

.ask-status {
  padding: 1px 6px;
  border-radius: var(--app-radius-sm, 4px);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
}

.ask-question {
  margin-top: 6px;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.ask-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ask-field-label {
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.ask-number {
  width: 100%;
}

.ask-field-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 150px), 1fr));
  gap: 6px;
  max-height: 200px;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 2px;
}

.ask-field-option {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 9px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.5;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

  &:hover:not(.is-disabled) { background: var(--td-bg-color-container-hover); }
  &.is-selected {
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-border);
  }
  &:has(input:focus-visible) { outline: 2px solid var(--td-brand-color); outline-offset: 1px; }
  &.is-disabled { opacity: .5; cursor: not-allowed; }
  input { position: absolute; width: 1px; height: 1px; opacity: 0; }
}

.ask-field-option-text { min-width: 0; overflow-wrap: anywhere; }
.ask-field-option-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 15px;
  height: 15px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-xs, 3px);
  color: transparent;
}
.is-selected .ask-field-option-mark {
  background: var(--td-brand-color);
  border-color: var(--td-brand-color);
  color: var(--td-text-color-anti);
}

// Filled, borderless fields, like the option rows and argument editor of the
// other prompts in the dock.
.ask-field :deep(.t-input),
.ask-text :deep(.t-textarea__inner) {
  min-height: 32px;
  border-color: transparent;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  box-shadow: none;
  font-size: var(--agent-step-summary-size, 13px);
  transition: background var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

  &:hover:not(.t-is-disabled):not(.t-input--focused):not(:focus) {
    border-color: transparent;
    background: var(--td-bg-color-secondarycontainer-hover);
  }
}

.ask-field :deep(.t-input.t-input--focused),
.ask-text :deep(.t-textarea__inner:focus) {
  border-color: color-mix(in srgb, var(--td-brand-color) 45%, var(--td-component-stroke));
  background: var(--td-bg-color-container);
  box-shadow: none;
}

.ask-field :deep(.t-input__inner::placeholder),
.ask-text :deep(.t-textarea__inner::placeholder) {
  color: var(--td-text-color-placeholder);
}

.ask-text {

  // The limit is far above a typical answer; the counter is noise here.
  :deep(.t-textarea__info_wrapper),
  :deep(.t-textarea__limit) {
    display: none;
  }
}

.ask-answer {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: var(--app-radius-md, 8px);
  background: var(--td-bg-color-secondarycontainer);
}

.ask-answer-label {
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.ask-answer-text {
  font-size: var(--agent-step-text-size, 14px);
  line-height: 1.6;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.ask-required {
  margin-left: 2px;
  color: var(--td-error-color);
}
</style>
