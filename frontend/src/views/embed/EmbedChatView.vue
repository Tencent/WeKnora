<template>
  <EmbedChatCore
    :session-id="sessionId"
    :channel-id="channelId"
    :token="token"
    :agent-id="agentId"
    :kb-ids="kbIds"
    @messages-change="(has: boolean) => emit('messages-change', has)"
  />
</template>

<script setup lang="ts">
import { defineAsyncComponent } from 'vue'

const EmbedChatCore = defineAsyncComponent(() => import('@/views/embed/EmbedChatCore.vue'))

defineProps<{
  sessionId: string
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
}>()

const emit = defineEmits<{
  (e: 'messages-change', hasMessages: boolean): void
}>()
</script>
