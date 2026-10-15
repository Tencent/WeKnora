<template>
  <div v-if="interactive" class="prompt-panel" @click.stop>
    <div class="prompt-question">{{ $t('agentStream.toolApproval.dockQuestion', { target: targetLabel }) }}</div>
    <div v-if="descriptionParts.head" class="prompt-desc">{{ descriptionParts.head }}</div>
    <ul v-if="descriptionParts.items.length" class="prompt-desc-items">
      <li v-for="(item, i) in descriptionParts.items" :key="i">{{ item }}</li>
    </ul>

    <div v-if="hasArgs" class="prompt-args">
      <button type="button" class="prompt-args-toggle" :aria-expanded="argsExpanded" @click="toggleArgs">
        <span>{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <t-icon class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
        <span v-if="!isJsonValid" class="args-status args-invalid">
          <t-icon name="error-circle" />
          {{ $t('agentStream.toolApproval.invalidJson') }}
        </span>
        <span v-else-if="argsDirty" class="args-status args-dirty">
          {{ $t('agentStream.toolApproval.argsModified') }}
        </span>
      </button>
      <t-textarea v-if="argsExpanded" v-model="argsText" class="prompt-args-input"
        :autosize="{ minRows: 2, maxRows: 10 }" placeholder="{}" />
    </div>

    <div class="prompt-footer">
      <div class="prompt-footer-meta">
        <span v-if="parked" class="action-timer">{{ parkedText }}</span>
        <span v-else-if="secondsLeft >= 0" class="action-timer" :class="timerClass">{{ countdownText }}</span>
      </div>
      <div class="prompt-footer-actions">
        <button type="button" class="prompt-btn" :disabled="submitting" @click="submit('reject')">
          {{ $t('agentStream.toolApproval.reject') }}
        </button>
        <button type="button" class="prompt-btn is-primary" :disabled="submitting || !isJsonValid"
          @click="submit('approve')">
          {{ $t('agentStream.toolApproval.approve') }}<span class="prompt-btn-key" aria-hidden="true">↵</span>
        </button>
      </div>
    </div>
  </div>

  <div v-else class="action-card interaction-inline" :class="{ 'action-pending': !resolved }">
    <div class="action-header no-results">
      <div class="action-title">
        <t-icon class="action-title-icon" :name="headerIcon" />
        <span class="action-name">{{ mainTitle }}</span>
      </div>
    </div>

    <div class="interaction-status-summary">
      <div class="results-summary-text">
        <template v-if="awaitingInDock">
          <span class="status-label">{{ $t('agentStream.promptDock.waiting') }}</span>
          <span class="inline-dot">·</span>
          <button type="button" class="inline-action is-primary" @click.stop="dock?.focus(pendingId)">
            {{ $t('agentStream.promptDock.handle') }}
          </button>
        </template>
        <span v-else class="status-label">{{ statusLine }}</span>
      </div>
    </div>

    <div v-if="hasArgs" class="interaction-status-summary is-clickable" @click="toggleArgs">
      <div class="results-summary-text args-toggle-row">
        <span>{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <t-icon class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
      </div>
    </div>

    <div v-if="hasArgs && argsExpanded" class="action-details">
      <div class="interaction-args-preview">{{ argsText }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, inject } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveToolApproval } from '@/api/mcp-service'
import { resolveEmbedToolApproval } from '@/api/embed'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { markPromptSubmitted, promptSubmission, usePromptDock } from '@/composables/usePromptDock'
import { isSubmitKey } from '@/utils/promptKeys'

const props = defineProps<{
  pendingId: string
  // dock: the approval form, shown above the composer. inline: the timeline row.
  variant?: 'dock' | 'inline'
  serviceName: string
  mcpToolName: string
  description?: string
  argsJson?: string
  timeoutSeconds?: number
  requestedAt?: number
  resolved?: boolean
  approved?: boolean
  resolveReason?: string
  // suspendable: past hotWaitSeconds the run is suspended rather than failed,
  // and the prompt stays answerable until expiresAt.
  suspendable?: boolean
  hotWaitSeconds?: number
  suspended?: boolean
  expiresAt?: string
  abandoned?: boolean
  embeddedMode?: boolean
  embedChannelId?: string
  embedToken?: string
  embedSessionId?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>()

const useEmbedApproval = () =>
  props.embeddedMode
  && props.embedChannelId
  && props.embedToken
  && props.embedSessionId
  && props.embedSessionSig
  && props.embedVisitorId

const { t } = useI18n()
const resumeSuspendedRun = inject(resumeSuspendedRunKey, undefined)
const dock = usePromptDock()

function formatJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

const initialArgs = formatJson(props.argsJson || '{}')
const argsText = ref(initialArgs)
const argsExpanded = ref(false)
const submitting = ref(false)
const { parked, secondsLeft, timerClass, countdownText, parkedText } = usePromptCountdown(props)

const submission = computed(() => promptSubmission(props.pendingId))
const open = computed(() => !props.resolved && !submission.value)
// Without a dock (a view that does not provide one) the row stays answerable.
const interactive = computed(() => open.value && (props.variant === 'dock' || !dock))
const awaitingInDock = computed(() => open.value && props.variant !== 'dock' && Boolean(dock?.isDocked(props.pendingId)))

const hasArgs = computed(() => argsText.value.trim().length > 0)

// A batch operation ends its description with one "- " line per item it acts
// on; those render as a list so every item stays visible.
const descriptionParts = computed(() => {
  const lines = (props.description || '').split('\n')
  const start = lines.findIndex((line) => line.startsWith('- '))
  if (start < 0 || !lines.slice(start).every((line) => line.startsWith('- '))) {
    return { head: (props.description || '').trim(), items: [] as string[] }
  }
  return {
    head: lines.slice(0, start).join('\n').trim(),
    items: lines.slice(start).map((line) => line.slice(2)),
  }
})

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

const headerIcon = computed(() => {
  if (!props.resolved) return 'secured'
  if (props.abandoned) return 'minus-circle'
  if (props.approved) return 'check-circle'
  return 'close-circle'
})

const targetLabel = computed(() => (
  t('agentStream.toolApproval.targetWithTool', {
    service: props.serviceName,
    tool: props.mcpToolName,
  })
))

const mainTitle = computed(() => {
  if (!props.resolved) {
    return t('agentStream.toolApproval.waiting', { target: targetLabel.value })
  }
  return t('agentStream.toolApproval.titleWithTarget', {
    service: props.serviceName,
    tool: props.mcpToolName,
  })
})

const statusLine = computed(() => {
  if (!props.resolved) {
    return submission.value ? t('agentStream.toolApproval.submitted') : t('agentStream.toolApproval.waitingStatus')
  }
  if (props.abandoned) return t('agentStream.toolApproval.abandonedTag')
  if (props.approved) return t('agentStream.toolApproval.approvedTag')
  return t('agentStream.toolApproval.rejectedTag')
})

function toggleArgs() {
  argsExpanded.value = !argsExpanded.value
}

const submit = async (decision: 'approve' | 'reject') => {
  if (!open.value || submitting.value) return
  submitting.value = true
  try {
    let modified: Record<string, unknown> | undefined
    if (decision === 'approve') {
      try {
        modified = JSON.parse(argsText.value || '{}') as Record<string, unknown>
      } catch {
        argsExpanded.value = true
        MessagePlugin.error(t('agentStream.toolApproval.invalidJson'))
        return
      }
    }
    const { resumed } = await (useEmbedApproval()
      ? resolveEmbedToolApproval(
        props.embedChannelId!,
        props.embedToken!,
        props.embedSessionId!,
        props.embedSessionSig!,
        props.embedVisitorId!,
        props.pendingId,
        {
          decision,
          modified_args: decision === 'approve' ? modified : undefined,
          reason: decision === 'reject' ? t('agentStream.toolApproval.userRejected') : undefined,
        },
      )
      : resolveToolApproval(props.pendingId, {
        decision,
        modified_args: decision === 'approve' ? modified : undefined,
        reason: decision === 'reject' ? t('agentStream.toolApproval.userRejected') : undefined,
      }))
    markPromptSubmitted(props.pendingId, { approved: decision === 'approve' })
    MessagePlugin.success(t('agentStream.toolApproval.submitted'))
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.toolApproval.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}

/** Enter approves while docked; in the args editor it needs Cmd/Ctrl. */
const handleKey = (e: KeyboardEvent): boolean => {
  if (!interactive.value || !isSubmitKey(e)) return false
  const inArgs = (e.target as HTMLElement | null)?.tagName === 'TEXTAREA'
  if (inArgs && !e.metaKey && !e.ctrlKey) return false
  if (!isJsonValid.value) return false
  void submit('approve')
  return true
}

defineExpose({ handleKey })
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';
</style>
