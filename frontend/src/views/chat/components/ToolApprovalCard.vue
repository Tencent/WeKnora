<template>
  <div class="action-card interaction-inline" :class="{ 'action-pending': !resolved }">
    <div class="action-header no-results">
      <div class="action-title">
        <t-icon class="action-title-icon" :name="headerIcon" />
        <span class="action-name">{{ mainTitle }}</span>
      </div>
    </div>

    <div class="interaction-status-summary">
      <div class="results-summary-text">
        <span v-if="resolved" class="status-label">{{ statusLine }}</span>
        <span v-if="!resolved" class="inline-actions">
          <button type="button" class="inline-action" :disabled="submitting" @click.stop="submit('reject')">
            {{ $t('agentStream.toolApproval.reject') }}
          </button>
          <span class="inline-dot">·</span>
          <button type="button" class="inline-action is-primary" :disabled="submitting || !isJsonValid"
            @click.stop="submit('approve')">
            {{ $t('agentStream.toolApproval.approve') }}
          </button>
        </span>
        <template v-if="!resolved && secondsLeft >= 0">
          <span class="inline-dot">·</span>
          <span class="action-timer" :class="timerClass">
            {{ formatCountdown(secondsLeft) }}
          </span>
        </template>
      </div>
    </div>

    <div v-if="!resolved && !argsExpanded && (!isJsonValid || argsDirty)" class="interaction-status-summary">
      <div class="results-summary-text">
        <span v-if="!isJsonValid" class="args-status args-invalid">
          <t-icon name="error-circle" />
          {{ $t('agentStream.toolApproval.invalidJson') }}
        </span>
        <span v-else-if="argsDirty" class="args-status args-dirty">
          {{ $t('agentStream.toolApproval.argsModified') }}
        </span>
      </div>
    </div>

    <div v-if="hasArgs" class="interaction-status-summary is-clickable" @click="toggleArgs">
      <div class="results-summary-text args-toggle-row">
        <span>{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <t-icon class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
      </div>
    </div>

    <div v-if="hasArgs && argsExpanded" class="action-details">
      <div v-if="!resolved && (!isJsonValid || argsDirty)" class="interaction-args-meta">
        <span v-if="!isJsonValid" class="args-status args-invalid">
          <t-icon name="error-circle" />
          {{ $t('agentStream.toolApproval.invalidJson') }}
        </span>
        <span v-else-if="argsDirty" class="args-status args-dirty">
          {{ $t('agentStream.toolApproval.argsModified') }}
        </span>
      </div>

      <div v-if="!resolved" class="interaction-args-block">
        <t-textarea v-model="argsText" class="interaction-args-input" :autosize="{ minRows: 2, maxRows: 8 }"
          placeholder="{}" @click.stop />
      </div>

      <div v-else class="interaction-args-preview">{{ argsText }}</div>
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
const argsExpanded = ref(false)
const submitting = ref(false)
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

const hasArgs = computed(() => argsText.value.trim().length > 0)

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

const headerIcon = computed(() => {
  if (!props.resolved) return 'secured'
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
  if (!props.resolved) return t('agentStream.toolApproval.waitingStatus')
  if (props.approved) return t('agentStream.toolApproval.approvedTag')
  return t('agentStream.toolApproval.rejectedTag')
})

function toggleArgs() {
  argsExpanded.value = !argsExpanded.value
}

function formatCountdown(s: number): string {
  if (s < 60) return t('agentStream.toolApproval.countdownShort', { seconds: s })
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
  if (props.resolved || submitting.value) return
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
  }
}
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';
</style>
