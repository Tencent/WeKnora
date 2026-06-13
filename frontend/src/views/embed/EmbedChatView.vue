<template>
  <EmbedChatCore
    :session-id="sessionId"
    :session-sig="sessionSig"
    :channel-id="channelId"
    :token="token"
    :agent-id="agentId"
    :kb-ids="kbIds"
    :welcome-message="welcomeMessage"
    :show-suggested-questions="showSuggestedQuestions"
    :allow-web-search="allowWebSearch"
    :agent-web-search-enabled="agentWebSearchEnabled"
    :allow-file-upload="allowFileUpload"
    :agent-image-upload-enabled="agentImageUploadEnabled"
    :use-session-header-title="useSessionHeaderTitle"
    :host-context="hostContext"
    @session-title="(title: string) => emit('session-title', title)"
    @messages-state="(has: boolean) => emit('messages-state', has)"
  />
</template>

<script setup lang="ts">
import { defineAsyncComponent } from 'vue'

const EmbedChatCore = defineAsyncComponent(() => import('@/views/embed/EmbedChatCore.vue'))

defineProps<{
  sessionId: string
  sessionSig: string
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  welcomeMessage?: string
  showSuggestedQuestions?: boolean
  allowWebSearch?: boolean
  agentWebSearchEnabled?: boolean
  allowFileUpload?: boolean
  agentImageUploadEnabled?: boolean
  useSessionHeaderTitle?: boolean
  hostContext?: Record<string, unknown>
}>()

const emit = defineEmits<{
  (e: 'session-title', title: string): void
  (e: 'messages-state', hasMessages: boolean): void
}>()
</script>
