<template>
  <div class="embed-page" :style="pageStyle">
    <div v-if="loadError" class="embed-error">{{ loadError }}</div>
    <template v-else-if="config">
      <div v-if="config.welcome_message && !hasMessages" class="embed-welcome">
        {{ config.welcome_message }}
      </div>
      <ChatView
        v-if="sessionId"
        :session_id="sessionId"
        :agentId="config.agent_id"
        :kbIds="[config.knowledge_base_id]"
        :embeddedMode="true"
        :embedChannelId="channelId"
        :embedToken="token"
      />
      <div v-else class="embed-loading">{{ $t('embedPublish.loading') }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import ChatView from '@/views/chat/index.vue'
import {
  createEmbedSession,
  getEmbedConfig,
  onEmbedHostContext,
  postEmbedReady,
  type EmbedChannelPublicConfig,
} from '@/api/embed'

const { t } = useI18n()
const route = useRoute()
const channelId = String(route.params.channelId || '')
const token = String(route.query.token || '')
const config = ref<EmbedChannelPublicConfig | null>(null)
const sessionId = ref('')
const loadError = ref('')
const hasMessages = ref(false)
const hostContext = ref<Record<string, unknown>>({})

const pageStyle = computed(() => {
  const color = config.value?.primary_color
  if (!color) return {}
  return { '--embed-primary': color } as Record<string, string>
})

let removeHostListener: (() => void) | null = null

watch(config, (cfg) => {
  if (cfg?.page_title) {
    document.title = cfg.page_title
  } else if (cfg?.name) {
    document.title = cfg.name
  }
}, { immediate: true })

onMounted(async () => {
  removeHostListener = onEmbedHostContext((payload) => {
    hostContext.value = { ...hostContext.value, ...payload }
  })

  if (!channelId || !token) {
    loadError.value = t('embedPublish.missingChannel')
    return
  }
  try {
    const res = await getEmbedConfig(channelId, token)
    if (!res?.success || !res.data) {
      loadError.value = t('embedPublish.invalidChannel')
      return
    }
    config.value = res.data
    const sessionRes = await createEmbedSession(channelId, token)
    sessionId.value = sessionRes?.data?.id || ''
    if (!sessionId.value) {
      loadError.value = t('embedPublish.sessionFailed')
      return
    }
    postEmbedReady(channelId)
  } catch (e: any) {
    loadError.value = e?.message || t('embedPublish.loadError')
  }
})

onUnmounted(() => {
  removeHostListener?.()
})
</script>

<style scoped>
.embed-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container, #fff);
}
.embed-page :deep(.control-btn.is-agent),
.embed-page :deep(.t-button--theme-primary) {
  --td-brand-color: var(--embed-primary, var(--td-brand-color));
}
.embed-welcome {
  padding: 12px 16px;
  font-size: 14px;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-border);
  border-left: 3px solid var(--embed-primary, var(--td-brand-color));
}
.embed-error,
.embed-loading {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
