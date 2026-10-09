<template>
  <div class="host-approval" :class="{ 'is-dangerous': host.reason === 'dangerous', 'is-resolved': resolved }">
    <div class="host-approval__header">
      <t-icon class="host-approval__icon" :name="headerIcon" />
      <span class="host-approval__title">{{ $t(titleKey) }}</span>
      <span v-if="!resolved && secondsLeft >= 0" class="host-approval__timer" :class="timerClass">
        {{ formatCountdown(secondsLeft) }}
      </span>
      <span v-else-if="resolved" class="host-approval__status">{{ statusLine }}</span>
    </div>

    <div class="host-approval__body">
      <div class="host-approval__label">{{ $t('agentStream.hostApproval.command') }}</div>
      <pre class="host-approval__code">{{ host.command }}</pre>
      <div v-if="host.cwd" class="host-approval__meta">
        {{ $t('agentStream.hostApproval.cwd') }}：<code>{{ host.cwd }}</code>
      </div>

      <template v-if="host.grant_path">
        <div class="host-approval__label">{{ $t('agentStream.hostApproval.grantPath') }}</div>
        <pre class="host-approval__code">{{ host.grant_path }}</pre>
        <t-radio-group v-if="!resolved && canNarrowAccess(host)" v-model="access" variant="default-filled" size="small">
          <t-radio-button value="write">{{ $t('agentStream.hostApproval.accessWrite') }}</t-radio-button>
          <t-radio-button value="read">{{ $t('agentStream.hostApproval.accessRead') }}</t-radio-button>
        </t-radio-group>
      </template>

      <div v-if="host.first_attempt_ran && !resolved" class="host-approval__notice">
        <t-icon name="info-circle" />
        <span>{{ $t('agentStream.hostApproval.firstAttemptRan') }}</span>
      </div>

      <template v-if="!resolved && sessionRules.length">
        <div class="host-approval__label">{{ $t('agentStream.hostApproval.sessionRules') }}</div>
        <pre class="host-approval__code is-muted">{{ sessionRules.join('\n') }}</pre>
      </template>

      <template v-if="host.denial_snippet">
        <button type="button" class="host-approval__toggle" @click="snippetExpanded = !snippetExpanded">
          <t-icon :name="snippetExpanded ? 'chevron-down' : 'chevron-right'" />
          {{ $t('agentStream.hostApproval.snippet') }}
        </button>
        <pre v-if="snippetExpanded" class="host-approval__code is-muted">{{ host.denial_snippet }}</pre>
      </template>
    </div>

    <div v-if="!resolved" class="host-approval__actions">
      <t-button variant="outline" :disabled="submitting" @click="submit('reject', 'once')">
        {{ $t('agentStream.hostApproval.reject') }}
      </t-button>
      <t-button v-if="host.allow_session" variant="outline" theme="primary" :disabled="submitting"
        @click="submit('approve', 'session')">
        {{ $t('agentStream.hostApproval.approveSession') }}
      </t-button>
      <t-button :theme="host.reason === 'dangerous' ? 'danger' : 'primary'" :loading="submitting"
        @click="submit('approve', 'once')">
        {{ $t('agentStream.hostApproval.approveOnce') }}
      </t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveToolApproval } from '@/api/mcp-service'
import {
  buildHostResolveBody,
  canNarrowAccess,
  clearComposerApproval,
  hostApprovalTitleKey,
  sessionRuleLabels,
  type HostApprovalAccess,
  type HostApprovalPayload,
  type HostApprovalScope,
} from './hostApproval'

const props = defineProps<{
  pendingId: string
  host: HostApprovalPayload
  timeoutSeconds?: number
  requestedAt?: number
  resolved?: boolean
  approved?: boolean
  resolveReason?: string
  resolveScope?: string
}>()

const { t } = useI18n()
const submitting = ref(false)
const snippetExpanded = ref(false)
const access = ref<HostApprovalAccess>(props.host.grant_access === 'read' ? 'read' : 'write')
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

const titleKey = computed(() => hostApprovalTitleKey(props.host.reason))
const sessionRules = computed(() => sessionRuleLabels(props.host))

const secondsLeft = computed(() => {
  if (props.resolved) return -1
  const deadline = (props.requestedAt || 0) * 1000 + (props.timeoutSeconds || 600) * 1000
  return Math.max(0, Math.floor((deadline - now.value) / 1000))
})

const timerClass = computed(() => {
  if (secondsLeft.value <= 30) return 'timer-critical'
  if (secondsLeft.value <= 120) return 'timer-warning'
  return ''
})

const headerIcon = computed(() => {
  if (!props.resolved) return props.host.reason === 'dangerous' ? 'error-circle' : 'secured'
  return props.approved ? 'check-circle' : 'close-circle'
})

const statusLine = computed(() => {
  if (!props.approved) return t('agentStream.hostApproval.rejectedTag')
  return props.resolveScope === 'session'
    ? t('agentStream.hostApproval.approvedSession')
    : t('agentStream.hostApproval.approvedOnce')
})

function formatCountdown(s: number): string {
  if (s < 60) return t('agentStream.toolApproval.countdownShort', { seconds: s })
  return `${Math.floor(s / 60)}:${(s % 60).toString().padStart(2, '0')}`
}

onMounted(() => {
  timer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

const submit = async (decision: 'approve' | 'reject', scope: HostApprovalScope) => {
  if (props.resolved || submitting.value) return
  submitting.value = true
  try {
    await resolveToolApproval(
      props.pendingId,
      buildHostResolveBody(decision, {
        scope,
        access: access.value,
        proposedAccess: props.host.grant_access,
        allowSession: props.host.allow_session,
        reason: decision === 'reject' ? t('agentStream.hostApproval.userRejected') : undefined,
      }),
    )
    // The resolved SSE event may be held back like the prompt was.
    clearComposerApproval(props.pendingId)
    MessagePlugin.success(t('agentStream.hostApproval.submitted'))
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.hostApproval.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="less">
.host-approval {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-xl);
  background: var(--td-bg-color-container);
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.08);

  &.is-dangerous {
    border-color: var(--td-error-color-5);
  }

  &.is-resolved {
    box-shadow: none;
    opacity: 0.85;
  }
}

.host-approval__header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.host-approval__icon {
  font-size: var(--app-text-2xl);
  color: var(--td-brand-color);

  .is-dangerous & {
    color: var(--td-error-color);
  }
}

.host-approval__title {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.host-approval__timer,
.host-approval__status {
  flex-shrink: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;

  &.timer-warning {
    color: var(--td-warning-color);
  }

  &.timer-critical {
    color: var(--td-error-color);
  }
}

.host-approval__body {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.host-approval__label {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.host-approval__code {
  margin: 0;
  padding: 8px 10px;
  max-height: 160px;
  overflow: auto;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--td-text-color-primary);

  &.is-muted {
    color: var(--td-text-color-secondary);
  }
}

.host-approval__meta {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    word-break: break-all;
  }
}

.host-approval__notice {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 10px;
  border-radius: var(--app-radius-md);
  background: var(--td-warning-color-1);
  color: var(--td-warning-color-8);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.host-approval__toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  align-self: flex-start;
  padding: 0;
  border: none;
  background: none;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  cursor: pointer;
}

.host-approval__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}
</style>
