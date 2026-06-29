<template>
  <div class="oauth-card" :class="cardClass">
    <!-- Status strip -->
    <div class="oauth-strip">
      <span class="oauth-strip-icon">
        <t-icon v-if="!resolved" name="lock-on" />
        <t-icon v-else-if="authorized" name="check-circle-filled" />
        <t-icon v-else name="close-circle-filled" />
      </span>
      <span class="oauth-strip-text">
        <template v-if="!resolved">{{ $t('agentStream.mcpOAuth.banner') }}</template>
        <template v-else-if="authorized">{{ $t('agentStream.mcpOAuth.authorizedTag') }}</template>
        <template v-else-if="timedOut">{{ $t('agentStream.mcpOAuth.timedOutTag') }}</template>
        <template v-else>{{ $t('agentStream.mcpOAuth.canceledTag') }}</template>
      </span>
      <span v-if="!resolved && secondsLeft >= 0" class="oauth-strip-timer" :class="timerClass">
        <t-icon name="time" />
        {{ formatCountdown(secondsLeft) }}
      </span>
    </div>

    <!-- Identity row -->
    <div class="oauth-identity">
      <span class="ident-service">{{ serviceName }}</span>
      <template v-if="mcpToolName">
        <t-icon name="chevron-right" class="ident-sep" />
        <span class="ident-tool">{{ mcpToolName }}</span>
      </template>
    </div>

    <div v-if="!resolved" class="oauth-desc">{{ $t('agentStream.mcpOAuth.desc') }}</div>

    <!-- Footer (pending) -->
    <div v-if="!resolved" class="oauth-footer">
      <span class="oauth-spacer" />
      <t-button
        theme="primary"
        size="small"
        :loading="authorizing"
        :disabled="authorizing"
        @click="authorize"
      >
        {{ $t('agentStream.mcpOAuth.authorize') }}
      </t-button>
    </div>

    <!-- Footer (resolved) -->
    <div v-else class="oauth-resolved-footer">
      <span v-if="resolveReason" class="oauth-resolved-reason">{{ resolveReason }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
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

const cardClass = computed(() => ({
  'is-resolved': !!props.resolved,
  'is-authorized': !!props.resolved && !!props.authorized,
  'is-failed': !!props.resolved && !props.authorized,
  'is-pending': !props.resolved,
}))

function formatCountdown(s: number): string {
  if (s < 60) return t('agentStream.mcpOAuth.countdown', { seconds: s })
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
    // Poll until the user authorizes (status flips) or closes the popup.
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
          await resolveMCPOAuth(props.pendingId, { service_id: props.serviceId })
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
@warning-rgb: 237, 122, 11;
@success-rgb: 7, 192, 95;
@danger-rgb: 232, 80, 91;

.oauth-card {
  --strip-color: var(--td-brand-color);
  --strip-rgb: 0, 82, 217;
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

  &.is-authorized {
    --strip-color: var(--td-success-color);
    --strip-rgb: @success-rgb;
    opacity: 0.94;
  }

  &.is-failed {
    --strip-color: var(--td-error-color);
    --strip-rgb: @danger-rgb;
    opacity: 0.94;
  }
}

.oauth-strip {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 12px 7px 14px;
  font-size: 12px;
  font-weight: 500;
  color: var(--strip-color);
  background: rgba(var(--strip-rgb), 0.06);
  border-bottom: 1px solid var(--td-component-stroke);

  .oauth-strip-icon {
    display: inline-flex;
    align-items: center;
    .t-icon {
      font-size: 14px;
    }
  }
  .oauth-strip-text {
    flex: 1;
    color: var(--strip-color);
  }
  .oauth-strip-timer {
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

.oauth-identity {
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

.oauth-desc {
  padding: 0 12px 4px 14px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.oauth-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px 12px 14px;
}

.oauth-spacer {
  flex: 1;
}

.oauth-resolved-footer {
  padding: 6px 12px 10px 14px;
  font-size: 12px;
  color: var(--td-text-color-secondary);

  .oauth-resolved-reason {
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
