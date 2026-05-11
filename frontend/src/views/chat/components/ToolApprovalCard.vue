<template>
  <div class="approval-card" :class="cardClass">
    <!-- Status strip -->
    <div class="approval-strip">
      <span class="approval-strip-icon">
        <t-icon v-if="!resolved" name="info-circle-filled" />
        <t-icon v-else-if="approved" name="check-circle-filled" />
        <t-icon v-else name="close-circle-filled" />
      </span>
      <span class="approval-strip-text">
        <template v-if="!resolved">{{ $t('agentStream.toolApproval.banner') }}</template>
        <template v-else-if="approved">{{ $t('agentStream.toolApproval.approvedTag') }}</template>
        <template v-else>{{ $t('agentStream.toolApproval.rejectedTag') }}</template>
      </span>
      <span v-if="!resolved && secondsLeft >= 0" class="approval-strip-timer" :class="timerClass">
        <t-icon name="time" />
        {{ formatCountdown(secondsLeft) }}
      </span>
    </div>

    <!-- Identity row -->
    <div class="approval-identity">
      <span class="ident-service">{{ serviceName }}</span>
      <t-icon name="chevron-right" class="ident-sep" />
      <span class="ident-tool">{{ mcpToolName }}</span>
    </div>

    <div v-if="description" class="approval-desc">{{ description }}</div>

    <!-- Args (editable while pending, read-only after resolve) -->
    <div class="approval-args">
      <div class="approval-args-label">
        <span class="args-label-text">{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <span v-if="!resolved && !isJsonValid" class="args-status args-invalid">
          <t-icon name="error-circle" /> {{ $t('agentStream.toolApproval.invalidJson') }}
        </span>
        <span v-else-if="!resolved && isJsonValid && argsDirty" class="args-status args-dirty">
          {{ $t('agentStream.toolApproval.argsModified') }}
        </span>
      </div>
      <t-textarea
        v-if="!resolved"
        v-model="argsText"
        class="approval-args-input"
        :autosize="{ minRows: 3, maxRows: 14 }"
        placeholder="{}"
      />
      <pre v-else class="approval-args-readonly"><code>{{ argsText }}</code></pre>
    </div>

    <!-- Footer (pending) -->
    <div v-if="!resolved" class="approval-footer">
      <span class="approval-spacer" />
      <t-button
        theme="default"
        variant="outline"
        size="small"
        :loading="submitting && pendingDecision === 'reject'"
        :disabled="submitting"
        @click="submit('reject')"
      >
        {{ $t('agentStream.toolApproval.reject') }}
      </t-button>
      <t-button
        theme="primary"
        size="small"
        :loading="submitting && pendingDecision === 'approve'"
        :disabled="submitting || !isJsonValid"
        @click="submit('approve')"
      >
        {{ $t('agentStream.toolApproval.approve') }}
      </t-button>
    </div>

    <!-- Footer (resolved) -->
    <div v-else class="approval-resolved-footer">
      <span v-if="resolveReason" class="approval-resolved-reason">{{ resolveReason }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveToolApproval } from '@/api/mcp-service'

const props = defineProps<{
  pendingId: string
  serviceName: string
  mcpToolName: string
  description?: string
  argsJson?: string
  timeoutSeconds?: number
  requestedAt?: number
  resolved?: boolean
  approved?: boolean
  resolveReason?: string
}>()

const { t } = useI18n()

function formatJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

const initialArgs = formatJson(props.argsJson || '{}')
const argsText = ref(initialArgs)
const submitting = ref(false)
const pendingDecision = ref<'approve' | 'reject' | null>(null)
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

const isJsonValid = computed(() => {
  if (!argsText.value.trim()) return true
  try {
    JSON.parse(argsText.value)
    return true
  } catch {
    return false
  }
})

const argsDirty = computed(() => argsText.value.trim() !== initialArgs.trim())

const deadline = computed(() => {
  const base = (props.requestedAt || 0) * 1000
  const add = (props.timeoutSeconds || 600) * 1000
  return base + add
})

const secondsLeft = computed(() => {
  if (props.resolved) return -1
  return Math.max(0, Math.floor((deadline.value - now.value) / 1000))
})

const timerClass = computed(() => {
  if (secondsLeft.value <= 30) return 'timer-critical'
  if (secondsLeft.value <= 120) return 'timer-warning'
  return ''
})

const cardClass = computed(() => ({
  'is-resolved': !!props.resolved,
  'is-approved': !!props.resolved && !!props.approved,
  'is-rejected': !!props.resolved && !props.approved,
  'is-pending': !props.resolved,
}))

function formatCountdown(s: number): string {
  if (s < 60) return t('agentStream.toolApproval.countdown', { seconds: s })
  const m = Math.floor(s / 60)
  const r = s % 60
  return `${m}:${r.toString().padStart(2, '0')}`
}

onMounted(() => {
  timer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

const submit = async (decision: 'approve' | 'reject') => {
  if (props.resolved) return
  submitting.value = true
  pendingDecision.value = decision
  try {
    let modified: Record<string, unknown> | undefined
    if (decision === 'approve') {
      try {
        modified = JSON.parse(argsText.value || '{}') as Record<string, unknown>
      } catch {
        MessagePlugin.error(t('agentStream.toolApproval.invalidJson'))
        return
      }
    }
    await resolveToolApproval(props.pendingId, {
      decision,
      modified_args: decision === 'approve' ? modified : undefined,
      reason: decision === 'reject' ? t('agentStream.toolApproval.userRejected') : undefined,
    })
    MessagePlugin.success(t('agentStream.toolApproval.submitted'))
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.toolApproval.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
    pendingDecision.value = null
  }
}
</script>

<style scoped lang="less">
@warning-rgb: 237, 122, 11;
@success-rgb: 7, 192, 95;
@danger-rgb: 232, 80, 91;

.approval-card {
  --strip-color: var(--td-warning-color);
  --strip-rgb: @warning-rgb;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.02);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, opacity 0.2s ease;
  display: flex;
  flex-direction: column;
  position: relative;

  &::before {
    content: '';
    position: absolute;
    inset: 0 auto 0 0;
    width: 3px;
    background: var(--strip-color);
    transition: background-color 0.2s ease;
  }

  &.is-pending {
    box-shadow: 0 1px 6px rgba(@warning-rgb, 0.08);
  }

  &.is-approved {
    --strip-color: var(--td-success-color);
    --strip-rgb: @success-rgb;
    opacity: 0.94;
  }

  &.is-rejected {
    --strip-color: var(--td-error-color);
    --strip-rgb: @danger-rgb;
    opacity: 0.94;
  }
}

.approval-strip {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 12px 7px 14px;
  font-size: 12px;
  font-weight: 500;
  color: var(--strip-color);
  background: rgba(var(--strip-rgb), 0.06);
  border-bottom: 1px solid var(--td-component-stroke);

  .approval-strip-icon {
    display: inline-flex;
    align-items: center;
    .t-icon {
      font-size: 14px;
    }
  }
  .approval-strip-text {
    flex: 1;
    color: var(--strip-color);
  }
  .approval-strip-timer {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    border-radius: 10px;
    background: rgba(0, 0, 0, 0.04);
    color: var(--td-text-color-secondary);
    font-variant-numeric: tabular-nums;
    font-weight: 500;

    .t-icon {
      font-size: 12px;
    }
    &.timer-warning {
      color: var(--td-warning-color);
      background: rgba(@warning-rgb, 0.1);
    }
    &.timer-critical {
      color: var(--td-error-color);
      background: rgba(@danger-rgb, 0.12);
      animation: timerPulse 1.2s ease-in-out infinite;
    }
  }
}

.approval-identity {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px 6px 14px;
  font-size: 13px;
  flex-wrap: wrap;

  .ident-service {
    color: var(--td-text-color-secondary);
    font-weight: 500;
  }
  .ident-sep {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
  }
  .ident-tool {
    color: var(--td-brand-color);
    font-weight: 600;
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
    font-size: 13px;
  }
}

.approval-desc {
  padding: 0 12px 4px 14px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.approval-args {
  padding: 8px 12px 0 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.approval-args-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;

  .args-label-text {
    color: var(--td-text-color-placeholder);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-size: 11px;
    font-weight: 500;
  }
  .args-status {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    margin-left: auto;
    font-size: 11px;
    .t-icon {
      font-size: 12px;
    }
  }
  .args-invalid {
    color: var(--td-error-color);
  }
  .args-dirty {
    color: var(--td-warning-color);
  }
}

.approval-args-input {
  :deep(.t-textarea__inner) {
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
    font-size: 12px;
    line-height: 1.7;
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-primary);
    padding: 8px 10px;
    transition: border-color 0.15s ease, box-shadow 0.15s ease;

    &:hover {
      border-color: var(--td-brand-color-hover);
    }
    &:focus,
    &:focus-visible {
      border-color: var(--td-brand-color);
      box-shadow: 0 0 0 2px rgba(@success-rgb, 0.12);
    }
  }
}

.approval-args-readonly {
  margin: 0;
  padding: 8px 10px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
  line-height: 1.7;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 180px;
  overflow: auto;
}

.approval-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px 12px 14px;
}

.approval-spacer {
  flex: 1;
}

.approval-resolved-footer {
  padding: 6px 12px 10px 14px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  min-height: 0;

  .approval-resolved-reason {
    color: var(--td-text-color-secondary);
  }

  &:empty {
    display: none;
  }
}

@keyframes timerPulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}
</style>
