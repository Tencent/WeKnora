<template>
  <div class="embed-page">
    <div v-if="loadError" class="embed-error">{{ loadError }}</div>
    <template v-else-if="config">
      <div v-if="config.welcome_message && messages.length === 0" class="embed-welcome">
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
      <div v-else class="embed-loading">Loading...</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import ChatView from '@/views/chat/index.vue'
import { createEmbedSession, getEmbedConfig, type EmbedChannelPublicConfig } from '@/api/embed'

const route = useRoute()
const channelId = String(route.params.channelId || '')
const token = String(route.query.token || '')
const config = ref<EmbedChannelPublicConfig | null>(null)
const sessionId = ref('')
const loadError = ref('')
const messages = ref<unknown[]>([])

onMounted(async () => {
  if (!channelId || !token) {
    loadError.value = 'Missing embed channel or token'
    return
  }
  try {
    const res = await getEmbedConfig(channelId, token)
    if (!res?.success || !res.data) {
      loadError.value = 'Invalid embed channel'
      return
    }
    config.value = res.data
    const sessionRes = await createEmbedSession(channelId, token)
    sessionId.value = sessionRes?.data?.id || ''
    if (!sessionId.value) {
      loadError.value = 'Failed to start chat session'
    }
  } catch (e: any) {
    loadError.value = e?.message || 'Failed to load embed widget'
  }
})
</script>

<style scoped>
.embed-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container, #fff);
}
.embed-welcome {
  padding: 12px 16px;
  font-size: 14px;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-border);
}
.embed-error,
.embed-loading {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
