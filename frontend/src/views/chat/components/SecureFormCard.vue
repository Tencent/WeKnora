<template>
  <div v-if="interactive" class="prompt-panel" @click.stop>
    <div class="prompt-question">{{ request.title }}</div>
    <p class="secure-form-domains">
      {{ $t('agentStream.secureForm.onlyFillInto', { domains: shownDomains.join(', ') }) }}
    </p>
    <p v-if="warning" class="secure-form-warning" role="alert">{{ warningText }}</p>
    <p v-else class="secure-form-origin">
      {{ $t('agentStream.secureForm.observedAt', { origin: request.observed_origin }) }}
    </p>

    <div v-if="login && vaultItems.length" class="ask-field">
      <span class="ask-field-label">{{ $t('agentStream.secureForm.savedAccount') }}</span>
      <t-select v-model="vaultSelection" size="small" :disabled="submitting"
        :popup-props="{ overlayClassName: 'wk-popover' }">
        <t-option v-for="item in vaultItems" :key="item.id" :value="item.id"
          :label="`${item.title} · ${item.username || '—'}`" />
        <t-option value="" :label="$t('agentStream.secureForm.useNew')" />
      </t-select>
    </div>

    <form class="secure-form-fields" autocomplete="off" @submit.prevent="submit(false)">
      <template v-if="choice !== 'saved'">
        <label v-if="parts.includes('username')" class="ask-field">
          <span class="ask-field-label">{{ $t('agentStream.secureForm.username') }}<span class="ask-required">*</span></span>
          <t-input v-model="values.username" size="small" autocomplete="off" :maxlength="256" :disabled="submitting" />
        </label>
        <label v-if="parts.includes('password')" class="ask-field">
          <span class="ask-field-label">{{ $t('agentStream.secureForm.password') }}<span class="ask-required">*</span></span>
          <t-input v-model="values.password" type="password" size="small" autocomplete="new-password"
            :disabled="submitting" />
        </label>
      </template>
      <label v-for="field in extra" :key="field.name" class="ask-field">
        <span class="ask-field-label">{{ field.label || field.name }}<span v-if="field.required" class="ask-required">*</span></span>
        <t-input v-model="values[field.name]" size="small" :type="inputTypeFor(field.type)"
          :autocomplete="autocompleteFor(field.type)" :disabled="submitting" />
      </label>
    </form>

    <t-radio-group v-if="login && choice !== 'saved'" v-model="saveChoice" size="small" :disabled="submitting">
      <t-radio v-if="canSave" value="save">{{ $t('agentStream.secureForm.saveToVault') }}</t-radio>
      <t-radio value="use_once">{{ $t('agentStream.secureForm.useOnce') }}</t-radio>
    </t-radio-group>

    <div class="prompt-footer">
      <div class="prompt-footer-meta">
        <span v-if="parked" class="action-timer">{{ parkedText }}</span>
        <span v-else-if="secondsLeft >= 0" class="action-timer" :class="timerClass">{{ countdownText }}</span>
      </div>
      <div class="prompt-footer-actions">
        <button type="button" class="prompt-btn" :disabled="submitting" @click="submit(true)">
          {{ $t('agentStream.secureForm.decline') }}
        </button>
        <button type="button" class="prompt-btn is-primary" :disabled="submitting || !canSubmit" @click="submit(false)">
          {{ $t('agentStream.secureForm.fill') }}<span class="prompt-btn-key" aria-hidden="true">↵</span>
        </button>
      </div>
    </div>
  </div>

  <div v-else class="action-card interaction-inline secure-form-inline" :class="{ 'action-pending': open }">
    <div class="action-header no-results">
      <div class="action-title">
        <ChatTimelineIcon class="action-title-icon" :name="headerIcon" :active="open" />
        <span class="action-name">{{ request.title }}</span>
        <span class="ask-status" role="status">
          {{ awaitingInDock ? $t('agentStream.promptDock.waiting') : statusLine }}
        </span>
        <button v-if="awaitingInDock" type="button" class="inline-action is-primary" @click.stop="dock?.focus(pendingId)">
          {{ $t('agentStream.promptDock.handle') }}
        </button>
      </div>
    </div>
    <div v-if="trail" class="ask-question">{{ trail }}</div>
  </div>
</template>

<script setup lang="ts">
import ChatTimelineIcon from '@/components/ChatTimelineIcon.vue'
import { computed, inject, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { listCredentialVault, submitSecureForm } from '@/api/credentialVault'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { markPromptSubmitted, promptSubmission, usePromptDock } from '@/composables/usePromptDock'
import {
  allowsSave, autocompleteFor, buildSecureFormSubmission, canSubmitSecureForm, fillDomains, formParts,
  inputTypeFor, isLogin, originWarning, type CredentialVaultItem, type SecureFormChoice, type SecureFormRequest,
} from '@/utils/secureForm'
import { isSubmitKey } from '@/utils/promptKeys'

const props = defineProps<{
  pendingId: string
  // dock: the credential form, shown above the composer. inline: the timeline row.
  variant?: 'dock' | 'inline'
  request?: SecureFormRequest
  timeoutSeconds?: number
  hotWaitSeconds?: number
  suspendable?: boolean
  suspended?: boolean
  expiresAt?: string
  requestedAt?: number
  resolved?: boolean
  answered?: boolean
  resolveReason?: string
  timedOut?: boolean
  canceled?: boolean
  abandoned?: boolean
}>()

const { t } = useI18n()
const resumeSuspendedRun = inject(resumeSuspendedRunKey, undefined)
const dock = usePromptDock()
const { parked, secondsLeft, timerClass, countdownText, parkedText } = usePromptCountdown(props)

const request = computed<SecureFormRequest>(() => props.request || { title: '', site: '' })
const login = computed(() => isLogin(request.value))
const parts = computed(() => formParts(request.value))
const extra = computed(() => request.value.extra || [])
const canSave = computed(() => allowsSave(request.value))

const submission = computed(() => promptSubmission(props.pendingId))
const open = computed(() => !props.resolved && !submission.value)
// Without a dock (a view that does not provide one) the row stays answerable.
const interactive = computed(() => open.value && (props.variant === 'dock' || !dock))
const awaitingInDock = computed(() => open.value && props.variant !== 'dock' && Boolean(dock?.isDocked(props.pendingId)))

// Values live only in the docked form: never in a store, storage or console.
const values = reactive<Record<string, string>>({})
const vaultItems = ref<CredentialVaultItem[]>([])
const vaultSelection = ref('')
const saveChoice = ref<'save' | 'use_once'>('use_once')
const submitting = ref(false)

const choice = computed<SecureFormChoice>(() => (vaultSelection.value ? 'saved' : saveChoice.value))
const selectedItem = computed(() => vaultItems.value.find((i) => i.id === vaultSelection.value))
const shownDomains = computed(() => fillDomains(request.value, selectedItem.value))
const trail = computed(() => {
  const domains = fillDomains(request.value).join(', ')
  return domains ? t('agentStream.secureForm.onlyFillInto', { domains }) : ''
})

const warning = computed(() => originWarning(request.value))
const warningText = computed(() => {
  switch (warning.value) {
    case 'unknown': return t('agentStream.secureForm.originUnknown')
    case 'mismatch': return t('agentStream.secureForm.originMismatch', { origin: request.value.observed_origin })
    case 'insecure': return t('agentStream.secureForm.originInsecure')
    default: return ''
  }
})

const draftSubmission = () => buildSecureFormSubmission(request.value, {
  choice: choice.value, vaultItemId: vaultSelection.value || undefined, values,
})
const canSubmit = computed(() => canSubmitSecureForm(request.value, draftSubmission()))

const headerIcon = computed(() => {
  if (!props.resolved && !submission.value) return 'lock-on'
  return props.answered || (submission.value && !submission.value.declined) ? 'check-circle' : 'minus-circle'
})

const statusLine = computed(() => {
  if (!props.resolved) {
    if (!submission.value) return t('agentStream.promptDock.waiting')
    return submission.value.declined ? t('agentStream.secureForm.declined') : t('agentStream.secureForm.submittedHidden')
  }
  if (props.abandoned) return t('agentStream.toolApproval.abandonedTag')
  if (props.answered) return t('agentStream.secureForm.submittedHidden')
  if (props.timedOut) return t('agentStream.secureForm.timedOut')
  if (props.canceled) return t('agentStream.secureForm.canceled')
  return t('agentStream.secureForm.declined')
})

let alive = true

const clearValues = () => {
  for (const key of Object.keys(values)) delete values[key]
}

const loadVault = async () => {
  const prefill = props.request?.prefill?.username
  if (prefill && !values.username) values.username = prefill
  if (!login.value) return
  try {
    const { data } = await listCredentialVault(request.value.site)
    if (!alive || !interactive.value) return
    // No preselection: releasing a saved login takes an explicit choice.
    vaultItems.value = data
  } catch {
    if (alive) vaultItems.value = []
  }
}

watch(interactive, (on) => {
  if (on) void loadVault()
  else clearValues()
}, { immediate: true })

onBeforeUnmount(() => {
  alive = false
  clearValues()
})

const submit = async (decline: boolean) => {
  if (!open.value || submitting.value) return
  const body = decline ? { action: 'decline' as const } : draftSubmission()
  if (!decline && !canSubmitSecureForm(request.value, body as ReturnType<typeof draftSubmission>)) return
  submitting.value = true
  try {
    const { resumed } = await submitSecureForm(props.pendingId, body)
    markPromptSubmitted(props.pendingId, { declined: decline })
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    if (e?.response?.data?.saved || e?.saved) {
      MessagePlugin.warning(t('agentStream.secureForm.savedButClosed'))
      markPromptSubmitted(props.pendingId, { declined: false })
    } else {
      MessagePlugin.error(e?.response?.data?.error?.message || e?.error?.message || e?.message
        || t('agentStream.secureForm.submitFailed'))
    }
  } finally {
    clearValues()
    submitting.value = false
  }
}

/** Enter submits while the form is docked, except inside a select dropdown. */
const handleKey = (e: KeyboardEvent): boolean => {
  if (!interactive.value || !isSubmitKey(e)) return false
  const target = e.target as HTMLElement | null
  if (target?.closest('.t-select, .t-select__wrap')) return false
  void submit(false)
  return true
}

defineExpose({ handleKey })
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';

.secure-form-inline {
  min-width: 0;

  .action-title {
    column-gap: 8px;
    row-gap: 4px;
  }

  .action-name {
    font-size: var(--agent-step-summary-size, 13px);
  }
}

.secure-form-domains {
  margin: 0;
  font-size: var(--agent-step-summary-size, 13px);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.secure-form-origin {
  margin: 0;
  font-size: var(--app-text-xs, 12px);
  color: var(--td-text-color-secondary);
}

.secure-form-warning {
  margin: 0;
  padding: 6px 10px;
  border-radius: var(--app-radius-md, 8px);
  background: var(--td-error-color-1);
  color: var(--td-error-color);
  font-size: var(--app-text-xs, 12px);
}

.secure-form-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
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

.ask-field :deep(.t-input) {
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

.ask-field :deep(.t-input.t-input--focused) {
  border-color: color-mix(in srgb, var(--td-brand-color) 45%, var(--td-component-stroke));
  background: var(--td-bg-color-container);
  box-shadow: none;
}

.ask-question {
  margin-top: 6px;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.65;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.ask-required {
  margin-left: 2px;
  color: var(--td-error-color);
}

.ask-status {
  padding: 1px 6px;
  border-radius: var(--app-radius-sm, 4px);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
}
</style>
