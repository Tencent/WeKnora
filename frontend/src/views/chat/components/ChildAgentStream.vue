<template>
  <div ref="viewport" class="child-stream" @scroll="onScroll">
    <div class="child-stream-content">
      <p class="child-stream-status" role="status">{{ t(`agentStream.collaboration.status.${task.status}`) }}</p>
      <p v-if="loading && !messages.length">{{ t('common.loading') }}</p>
      <div v-if="failure" class="child-stream-error" role="alert">
        {{ failure }} <button type="button" @click="refresh">{{ t('common.retry') }}</button>
      </div>
      <template v-for="message in messages" :key="String(message.id)">
        <details v-if="message.role === 'user'" class="child-assignment">
          <summary>{{ t('agentStream.collaboration.assignment') }}</summary>
          <p>{{ message.content }}</p>
        </details>
        <AgentStreamDisplay v-else-if="message.role === 'assistant'" :session="message" :session-id="task.sessionId" />
      </template>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChatScrollFollow } from '@/composables/useChatScrollFollow'
import { getMessageList } from '@/api/chat'
import { useStream } from '@/api/chat/streame'
import { isAwaitingInput, useChatStreamHandler, type ChatMessage } from '@/composables/useChatStreamHandler'
import { isCollaborationTaskActive, type CollaborationTaskView } from '@/utils/collaborationDisplay'
const AgentStreamDisplay = defineAsyncComponent(() => import('./AgentStreamDisplay.vue'))
const props = defineProps<{ task: CollaborationTaskView; active?: boolean }>()
const { t } = useI18n()
const messages = reactive<ChatMessage[]>([])
const viewport = ref<HTMLElement | null>(null)
const loading = ref(false)
const isReplying = ref(false)
const currentAssistantMessageId = ref('')
const fullContent = ref('')
const failure = ref('')
let closed = false
let refreshing = false
let refreshRequested = false
let timer: ReturnType<typeof setTimeout> | undefined
const readingHistory = ref(false)
let historySignature = ''
const scrollFollow = useChatScrollFollow(viewport, readingHistory, computed(() => props.active !== false))
const { onScroll, scrollToBottom } = scrollFollow
let savedScrollTop = 0
watch(() => props.active, active => {
  if (active === false) savedScrollTop = viewport.value?.scrollTop || 0
  else void nextTick(() => {
    if (viewport.value) {
      viewport.value.scrollTop = readingHistory.value ? savedScrollTop : viewport.value.scrollHeight
      scrollFollow.rememberPosition()
    }
  })
})
const stream = useStream()
const handler = useChatStreamHandler({ messagesList: messages, loading, isReplying,
  currentAssistantMessageId, fullContent, isAgentStreamSession: () => true, scrollToBottom,
  preserveIncompleteStreamReactive: true })
stream.onChunk(data => { if (!closed) handler.processStreamChunk(data) })
async function refresh() {
  if (closed || refreshing || stream.isStreaming.value) return
  clearTimeout(timer)
  refreshing = true
  loading.value = true
  try {
    const response = await getMessageList({ session_id: props.task.sessionId, limit: 100, created_at: '' })
    if (closed) return
    const rows = (response?.data || []) as ChatMessage[]
    const signature = JSON.stringify(rows)
    if (signature !== historySignature) {
      messages.splice(0)
      await handler.handleMsgList(rows)
      historySignature = signature
    }
    failure.value = ''
    // Suspended messages are settled for rendering, but may resume on the same
    // message ID. Never mistake that presentation flag for a terminal task.
    const latest = [...messages].reverse().find(m => m.role === 'assistant')
    const waiting = isAwaitingInput(latest)
    const pending = latest && (!latest.is_completed || waiting) ? latest : undefined
    if (pending && (!waiting || props.task.status === 'running')) {
      currentAssistantMessageId.value = String(pending.id)
      if (waiting) handler.prepareResumedMessage(pending, String((pending.pending_prompt as any)?.id || ''))
      await stream.startStream({ session_id: props.task.sessionId, query: String(pending.id),
        method: 'GET', url: '/api/v1/sessions/continue-stream' })
      if (!closed && stream.error.value) failure.value = stream.error.value
    }
  } catch (error: any) {
    if (!closed) failure.value = error?.message || t('common.error')
  } finally {
    refreshing = false
    loading.value = false
    // Poll only while disconnected; token updates use the child SSE stream.
    if (!closed && (refreshRequested || isCollaborationTaskActive(props.task.status))) {
      timer = setTimeout(refresh, refreshRequested ? 0 : 2000)
    }
    refreshRequested = false
  }
}
watch(() => props.task.status, () => {
  if (refreshing) refreshRequested = true
  else if (!stream.isStreaming.value) void refresh()
})
onMounted(refresh)
onUnmounted(() => { closed = true; clearTimeout(timer); stream.stopStream() })
</script>
<style scoped lang="less">
@import '../../../components/css/chat-scrollbar.less';
.child-stream { height: 100%; box-sizing: border-box; overflow-y: auto; overscroll-behavior-y: contain; padding: 18px 16px 24px; font-size: var(--app-text-base); .chat-scrollbar(); }
.child-stream-status { color: var(--td-text-color-placeholder); margin: 0 0 18px; }
.child-assignment { margin-bottom: 22px; padding: 12px 16px; background: var(--td-bg-color-secondarycontainer); border-radius: var(--app-radius-lg); }
.child-assignment summary { cursor: pointer; color: var(--td-text-color-secondary); }
.child-assignment p { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.6; }
.child-stream-error { color: var(--td-error-color); margin: 12px 0; }
</style>
