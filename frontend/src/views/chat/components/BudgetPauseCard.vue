<template>
  <div v-if="interactive" class="prompt-panel budget-panel" @click.stop>
    <div class="budget-summary">
      <div class="budget-reason">{{ reasonText }}</div>
      <div class="budget-meter" role="group" :aria-label="reasonText">
        <div v-for="row in usageRows" :key="row.key" class="budget-meter__row">
          <span class="budget-meter__label">{{ row.label }}</span>
          <span class="budget-meter__value">{{ row.used }} / {{ row.limit }}</span>
          <span class="budget-meter__track" aria-hidden="true">
            <span class="budget-meter__fill" :class="`is-${row.state}`" :style="{ width: row.percent }" />
          </span>
        </div>
      </div>
    </div>

    <div v-if="canExtend" class="budget-extension">
      <label class="budget-extension__label" :for="`${pendingId}-extension`">
        {{ t('agentStream.budget.extensionLabel') }}
      </label>
      <t-input-number
        :id="`${pendingId}-extension`"
        v-model="extensionValue"
        :min="1"
        :max="extensionMax"
        :step="extensionStep"
        theme="normal"
        size="small"
        :disabled="submitting"
      />
      <span class="budget-extension__unit">{{ extensionUnit }}</span>
    </div>

    <div class="prompt-footer budget-footer">
      <div class="prompt-footer-meta">
        <span v-if="parked" class="action-timer">{{ parkedText }}</span>
        <span v-else-if="secondsLeft >= 0" class="action-timer" :class="timerClass">{{ countdownText }}</span>
      </div>
      <div class="prompt-footer-actions">
        <button type="button" class="prompt-btn budget-cancel" :disabled="submitting" @click="submit('cancel')">
          {{ t('agentStream.budget.cancel') }}
        </button>
        <button type="button" class="prompt-btn" :disabled="submitting" @click="submit('finish')">
          {{ t('agentStream.budget.finish') }}
        </button>
        <button v-if="canExtend" type="button" class="prompt-btn is-primary" :disabled="submitting"
          @click="submit('extend')">
          {{ t('agentStream.budget.extend') }}
        </button>
      </div>
    </div>
  </div>

  <div v-else class="action-card interaction-inline budget-inline" :class="{ 'action-pending': open }">
    <div class="action-header no-results">
      <div class="action-title">
        <ChatTimelineIcon class="action-title-icon" :name="headerIcon" :active="open" />
        <span class="action-name">{{ t('agentStream.budget.title') }}</span>
        <span class="budget-status" role="status">
          {{ awaitingInDock ? t('agentStream.promptDock.waiting') : statusLine }}
        </span>
        <button v-if="awaitingInDock" type="button" class="inline-action is-primary" @click.stop="dock?.focus(pendingId)">
          {{ t('agentStream.promptDock.handle') }}
        </button>
      </div>
    </div>
    <div class="budget-inline__reason">{{ reasonText }}</div>
    <div v-if="resolvedSummary" class="budget-inline__summary">{{ resolvedSummary }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ChatTimelineIcon from './ChatTimelineIcon.vue'
import { resolveAgentPrompt } from '@/api/mcp-service'
import { resolveEmbedAgentPrompt } from '@/api/embed'
import { markPromptSubmitted, promptSubmission, usePromptDock } from '@/composables/usePromptDock'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { isSubmitKey } from '@/utils/promptKeys'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'

const props = withDefaults(defineProps<{
  pendingId: string
  request?: Record<string, any>
  variant?: 'dock' | 'inline'
  timeoutSeconds?: number
  hotWaitSeconds?: number
  suspendable?: boolean
  suspended?: boolean
  expiresAt?: string
  requestedAt?: number
  resolved?: boolean
  answered?: boolean
  canceled?: boolean
  abandoned?: boolean
  resolveReason?: string
  response?: Record<string, any>
  embeddedMode?: boolean
  embedChannelId?: string
  embedToken?: string
  embedSessionId?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>(), {
  variant: 'inline',
  request: () => ({}),
})

const { t } = useI18n()
const dock = usePromptDock()
const resumeSuspendedRun = inject(resumeSuspendedRunKey, undefined)
const submitting = ref(false)
const submittedLocal = computed(() => promptSubmission(props.pendingId))
const open = computed(() => !props.resolved && !submittedLocal.value)
const interactive = computed(() => props.variant === 'dock' && open.value)
const awaitingInDock = computed(() => open.value && Boolean(dock?.isDocked(props.pendingId)))

const {
  parked, parkedText, secondsLeft, countdownText, timerClass,
} = usePromptCountdown(props)

type BudgetRow = {
  key: 'tokens' | 'time' | 'writes'
  label: string
  used: string
  limit: string
  percent: string
  state: 'ok' | 'near' | 'full'
}

const used = computed(() => (props.request?.used || {}) as Record<string, number>)
const limit = computed(() => (props.request?.limit || {}) as Record<string, number>)
const suggested = computed(() => (props.request?.suggested_extension || {}) as Record<string, number>)

const numberText = (n: number) => new Intl.NumberFormat().format(Math.max(0, Number(n) || 0))
const rowState = (ratio: number): BudgetRow['state'] => ratio >= 1 ? 'full' : ratio >= 0.8 ? 'near' : 'ok'
const rowPercent = (current: number, max: number) => `${Math.min(100, Math.max(0, max > 0 ? current / max * 100 : 0))}%`

const usageRows = computed<BudgetRow[]>(() => {
  const rows: BudgetRow[] = []
  if (Number(limit.value.max_total_tokens) > 0) {
    const current = Number(used.value.total_tokens) || 0
    const max = Number(limit.value.max_total_tokens) || 0
    rows.push({
      key: 'tokens', label: t('agentStream.budget.tokens'), used: numberText(current), limit: numberText(max),
      percent: rowPercent(current, max), state: rowState(current / max),
    })
  }
  if (Number(limit.value.max_wall_time_sec) > 0) {
    const current = Number(used.value.wall_time_sec) || 0
    const max = Number(limit.value.max_wall_time_sec) || 0
    rows.push({
      key: 'time', label: t('agentStream.budget.time'), used: `${numberText(current)}s`, limit: `${numberText(max)}s`,
      percent: rowPercent(current, max), state: rowState(current / max),
    })
  }
  if (Number(limit.value.max_write_actions) > 0) {
    const current = Number(used.value.write_actions) || 0
    const max = Number(limit.value.max_write_actions) || 0
    rows.push({
      key: 'writes', label: t('agentStream.budget.writes'), used: numberText(current), limit: numberText(max),
      percent: rowPercent(current, max), state: rowState(current / max),
    })
  }
  return rows
})

const reasonKey = computed(() => {
  const reason = String(props.request?.reason || '')
  if (reason.includes('time')) return 'time'
  if (reason.includes('write')) return 'writes'
  return 'tokens'
})
const reasonText = computed(() => t(`agentStream.budget.reasons.${reasonKey.value}`))

const extensionKey = computed<'max_total_tokens' | 'max_wall_time_sec' | 'max_write_actions'>(() => {
  if (reasonKey.value === 'time') return 'max_wall_time_sec'
  if (reasonKey.value === 'writes') return 'max_write_actions'
  return 'max_total_tokens'
})
const extensionSuggested = computed(() => Math.max(1, Number(suggested.value[extensionKey.value]) || 1))
const extensionMax = computed(() => Math.max(extensionSuggested.value * 20, 1_000_000))
const extensionStep = computed(() => reasonKey.value === 'writes' ? 1 : Math.max(1, Math.round(extensionSuggested.value / 2)))
const extensionUnit = computed(() => {
  if (reasonKey.value === 'time') return t('agentStream.budget.secondsUnit')
  if (reasonKey.value === 'writes') return t('agentStream.budget.actionsUnit')
  return t('agentStream.budget.tokensUnit')
})
const canExtend = computed(() => Object.values(suggested.value).some(v => Number(v) > 0))
const extensionValue = ref(extensionSuggested.value)
watch(extensionSuggested, (value) => { extensionValue.value = value })

const headerIcon = computed(() => props.resolved ? (props.canceled ? 'minus-circle' : 'check-circle') : 'time')
const statusLine = computed(() => {
  if (!open.value) return ''
  return t('agentStream.budget.unansweredTag')
})
const resolvedSummary = computed(() => {
  const answer = props.resolved ? props.response : submittedLocal.value
  if (!answer) return ''
  const action = String(answer.action || '')
  if (action === 'extend') return t('agentStream.budget.extended')
  if (action === 'finish') return t('agentStream.budget.finished')
  if (action === 'cancel') return t('agentStream.budget.canceled')
  return ''
})

const embedTarget = () =>
  props.embeddedMode && props.embedChannelId && props.embedToken && props.embedSessionId
  && props.embedSessionSig && props.embedVisitorId

const submit = async (action: 'extend' | 'finish' | 'cancel') => {
  if (!open.value || submitting.value) return
  const extension = action === 'extend'
    ? { [extensionKey.value]: Math.max(1, Number(extensionValue.value) || extensionSuggested.value) }
    : undefined
  const response: Record<string, unknown> = action === 'extend' ? { action, extension } : { action }
  submitting.value = true
  try {
    const { resumed } = await (embedTarget()
      ? resolveEmbedAgentPrompt(
        props.embedChannelId!, props.embedToken!, props.embedSessionId!, props.embedSessionSig!,
        props.embedVisitorId!, props.pendingId, 'budget_pause', response,
      )
      : resolveAgentPrompt(props.pendingId, 'budget_pause', response))
    markPromptSubmitted(props.pendingId, response)
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.error?.message || e?.message
      || t('agentStream.budget.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}

const handleKey = (e: KeyboardEvent): boolean => {
  if (!interactive.value) return false
  if (isSubmitKey(e) && canExtend.value) {
    void submit('extend')
    return true
  }
  return false
}

defineExpose({ handleKey })
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';

.budget-summary {
  display: grid;
  gap: 10px;
}

.budget-reason,
.budget-inline__reason {
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.65;
  color: var(--td-text-color-primary);
}

.budget-inline__summary {
  margin-top: 6px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
}

.budget-status {
  padding: 1px 6px;
  border-radius: var(--app-radius-sm, 4px);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
}

.budget-meter {
  display: grid;
  gap: 8px;
}

.budget-meter__row {
  display: grid;
  grid-template-columns: minmax(64px, 0.8fr) minmax(104px, 1fr) minmax(120px, 2fr);
  align-items: center;
  gap: 10px;
  font-size: var(--app-text-xs, 12px);
}

.budget-meter__label { color: var(--td-text-color-secondary); }
.budget-meter__value { color: var(--td-text-color-primary); font-variant-numeric: tabular-nums; }
.budget-meter__track {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
}
.budget-meter__fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--td-brand-color);
  transition: width var(--app-motion-fast) ease;
  &.is-near { background: var(--td-warning-color); }
  &.is-full { background: var(--td-error-color); }
}

.budget-extension {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  font-size: var(--app-text-xs, 12px);
}
.budget-extension__label { color: var(--td-text-color-secondary); white-space: nowrap; }
.budget-extension__unit { color: var(--td-text-color-secondary); }
.budget-footer { margin-top: 12px; }
.budget-cancel { color: var(--td-error-color); }
</style>