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
          <button type="button" class="inline-action" :disabled="canceling || authorizing" @click="skip">
            {{ $t('agentStream.mcpOAuth.skip') }}
          </button>
          <span class="inline-dot">·</span>
          <button type="button" class="inline-action is-primary" :disabled="authorizing || canceling"
            @click="authorize">
            {{ $t('agentStream.mcpOAuth.authorize') }}
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  cancelMCPOAuth,
  getMCPOAuthAuthorizeURL,
  getMCPOAuthStatus,
  resolveMCPOAuth,
  MCP_OAUTH_CALLBACK_PATH,
} from '@/api/mcp-service'

const props = defineProps<{
  pendingId: string
  serviceId: string
  serviceName: string
  mcpToolName?: string
  timeoutSeconds?: number
  requestedAt?: number
  resolved?: boolean
  authorized?: boolean
  resolveReason?: string
  timedOut?: boolean
  canceled?: boolean
}>()

const { t } = useI18n()

const authorizing = ref(false)
const canceling = ref(false)
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | null = null
let poll: ReturnType<typeof setInterval> | null = null

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
  if (!props.resolved) return 'lock-on'
  if (props.authorized) return 'check-circle'
  return 'close-circle'
})

const targetLabel = computed(() => (
  props.mcpToolName
    ? t('agentStream.mcpOAuth.targetWithTool', { service: props.serviceName, tool: props.mcpToolName })
    : props.serviceName
))

const mainTitle = computed(() => {
  if (!props.resolved) {
    return t('agentStream.mcpOAuth.waiting', { target: targetLabel.value })
  }
  return props.mcpToolName
    ? t('agentStream.mcpOAuth.titleWithTool', { service: props.serviceName, tool: props.mcpToolName })
    : t('agentStream.mcpOAuth.titleWithService', { service: props.serviceName })
})

const statusLine = computed(() => {
  if (!props.resolved) return t('agentStream.mcpOAuth.waitingStatus')
  if (props.authorized) return t('agentStream.mcpOAuth.authorizedTag')
  if (props.timedOut) return t('agentStream.mcpOAuth.timedOutTag')
  return t('agentStream.mcpOAuth.canceledTag')
})

function formatCountdown(s: number): string {
  if (s < 60) return t('agentStream.mcpOAuth.countdownShort', { seconds: s })
  const m = Math.floor(s / 60)
  const r = s % 60
  return `${m}:${r.toString().padStart(2, '0')}`
}

function stopPoll() {
  if (poll) {
    clearInterval(poll)
    poll = null
  }
}

const skip = async () => {
  if (props.resolved || canceling.value) return
  canceling.value = true
  try {
    await cancelMCPOAuth(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.mcpOAuth.skipFailed')
    MessagePlugin.error(msg)
  } finally {
    canceling.value = false
  }
}

const authorize = async () => {
  if (props.resolved || authorizing.value) return
  authorizing.value = true
  try {
    const redirectUri = window.location.origin + MCP_OAUTH_CALLBACK_PATH
    const frontendRedirect = window.location.origin + '/'
    const authUrl = await getMCPOAuthAuthorizeURL(props.serviceId, {
      redirect_uri: redirectUri,
      frontend_redirect: frontendRedirect,
    })
    if (!authUrl) {
      MessagePlugin.error(t('agentStream.mcpOAuth.startFailed'))
      authorizing.value = false
      return
    }
    const popup = window.open(authUrl, 'mcp_oauth', 'width=600,height=720')
    poll = window.setInterval(async () => {
      const closed = !popup || popup.closed
      let ok = false
      try {
        ok = await getMCPOAuthStatus(props.serviceId)
      } catch {
        /* transient; keep polling */
      }
      if (ok) {
        stopPoll()
        try { popup?.close() } catch { /* cross-origin close may throw */ }
        try {
          await resolveMCPOAuth(props.pendingId, { service_id: props.serviceId, decision: 'authorize' })
          MessagePlugin.success(t('agentStream.mcpOAuth.authorizedToast'))
        } catch (e: any) {
          const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.mcpOAuth.resumeFailed')
          MessagePlugin.error(msg)
        }
        authorizing.value = false
      } else if (closed) {
        stopPoll()
        authorizing.value = false
      }
    }, 1500)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.mcpOAuth.startFailed')
    MessagePlugin.error(msg)
    authorizing.value = false
  }
}

onMounted(() => {
  clock = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (clock) clearInterval(clock)
  stopPoll()
})
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';
</style>
