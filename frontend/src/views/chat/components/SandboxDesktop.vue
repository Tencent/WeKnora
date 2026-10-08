<template>
  <div class="sandbox-desktop">
    <div ref="screenRef" class="sandbox-desktop__screen"
         @mousedown="onUserActivity" @keydown="onUserActivity" />

    <div v-if="status !== 'connected'" class="sandbox-desktop__overlay">
      <div class="sandbox-desktop__overlay-card">
        <t-icon v-if="status === 'starting'" name="loading" size="24px"
                class="sandbox-desktop__spinner" />
        <t-icon v-else-if="status === 'unsupported'" name="error-circle" size="28px" />
        <t-icon v-else-if="status === 'busy' || status === 'rebuilt'" name="info-circle" size="28px" />
        <t-icon v-else-if="status === 'paused' || (status === 'idle' && hasConnected)" name="time" size="28px" />
        <t-icon v-else name="desktop" size="28px" />
        <p class="sandbox-desktop__overlay-text">{{ statusText }}</p>
        <t-button v-if="actionLabel" size="small"
                  :theme="status === 'idle' || status === 'needs_provision' || status === 'paused' ? 'primary' : 'default'"
                  variant="outline" @click.stop="start">
          {{ actionLabel }}
        </t-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSandboxDesktop } from '@/composables/useSandboxDesktop'

const props = defineProps<{
  sessionId: string
  agentId?: string
  agentSourceTenantId?: string | number | null
}>()

const { t } = useI18n()
const screenRef = ref<HTMLElement | null>(null)

const desktop = useSandboxDesktop(
  toRef(props, 'sessionId'),
  toRef(props, 'agentId'),
  toRef(props, 'agentSourceTenantId'),
)
const status = desktop.status

// Composable uses 'starting' on first paint (lookup in flight) and 'idle'
// for IDLE_DISCONNECTED. :key="sessionId" remounts this component, so the
// flag resets per session.
const hasConnected = ref(false)
watch(status, (next) => {
  if (next === 'connected') hasConnected.value = true
})

const statusText = computed(() => {
  switch (status.value) {
    case 'starting': return t('chat.sandbox.desktopStarting')
    case 'unsupported': return t('chat.sandbox.desktopUnsupported')
    case 'busy': return t('chat.sandbox.desktopBusy')
    case 'start_failed': return t('chat.sandbox.desktopStartFailed')
    case 'rebuilt': return t('chat.sandbox.desktopRebuilt')
    case 'needs_provision': return t('chat.sandbox.desktopNeedsProvision')
    case 'paused': return t('chat.sandbox.desktopPaused')
    case 'unauthorized': return t('chat.sandbox.authRevoked')
    case 'disconnected': return t('chat.sandbox.desktopDisconnected')
    case 'error': return t('chat.sandbox.desktopDisconnected')
    case 'idle':
      return hasConnected.value
        ? t('chat.sandbox.desktopIdleDisconnected')
        : t('chat.sandbox.desktopNotStarted')
    default: return t('chat.sandbox.desktopNotStarted')
  }
})

// unsupported 是配置问题，重试一万次也一样，所以不给按钮。
const actionLabel = computed(() => {
  switch (status.value) {
    case 'idle':
      return hasConnected.value
        ? t('chat.sandbox.desktopRetry')
        : t('chat.sandbox.desktopStart')
    case 'paused': return t('chat.sandbox.desktopStart')
    case 'needs_provision': return t('chat.sandbox.desktopCreateAndStart')
    case 'start_failed':
    case 'rebuilt':
    case 'busy':
    case 'disconnected':
    case 'error': return t('chat.sandbox.desktopRetry')
    default: return ''
  }
})

function start() {
  if (!screenRef.value) return
  desktop.connect(screenRef.value, { provision: true })
}

function connectLookup() {
  if (!screenRef.value) {
    void nextTick(() => {
      if (screenRef.value) desktop.connect(screenRef.value, { provision: false })
    })
    return
  }
  desktop.connect(screenRef.value, { provision: false })
}

onMounted(() => {
  connectLookup()
})

// 兜底的活动信号：只有后端 opcode 解析降级时才真正生效，composable 内部已按
// 30 秒 debounce。
function onUserActivity() {
  desktop.reportActivity()
}

onBeforeUnmount(() => desktop.dispose())

defineExpose({ start })
</script>

<style scoped lang="less">
.sandbox-desktop {
  position: relative;
  width: 100%;
  height: 100%;
  background: #1e1e1e;
  overflow: hidden;
}

.sandbox-desktop__screen {
  width: 100%;
  height: 100%;
}

.sandbox-desktop__overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(30, 30, 30, 0.92);
}

.sandbox-desktop__overlay-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  max-width: 320px;
  padding: 24px;
  text-align: center;
  color: #d8d8d8;
}

.sandbox-desktop__overlay-text {
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 1.6;
}

.sandbox-desktop__spinner {
  animation: wk-spin 1s linear infinite;
}

</style>
