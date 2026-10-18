<template>
  <div ref="viewport" class="child-stream" @scroll="onScroll">
    <div class="child-stream-content">
      <p v-if="loading && !messages.length">{{ t('common.loading') }}</p>
      <div v-if="failure" class="child-stream-error" role="alert">
        {{ failure }} <button type="button" @click="refresh">{{ t('common.retry') }}</button>
      </div>
      <template v-for="message in messages" :key="String(message.id)">
        <details v-if="message.role === 'user'" class="child-assignment">
          <summary>
            <span class="child-assignment-label">
              <TaskIcon class="child-assignment-icon" size="18px" aria-hidden="true" />
              {{ t('agentStream.collaboration.assignment') }}
            </span>
            <span v-if="message === latestAssignment" class="child-assignment-status" :class="task.status" role="status">
              {{ t(`agentStream.collaboration.status.${task.status}`) }}
            </span>
            <ChevronRightIcon class="child-assignment-chevron" size="16px" aria-hidden="true" />
          </summary>
          <div class="child-assignment-body">{{ message.content }}</div>
        </details>
        <AgentStreamDisplay v-else-if="message.role === 'assistant'" :session="message" :session-id="task.sessionId" artifacts-in-drawer />
      </template>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronRightIcon, TaskIcon } from 'tdesign-icons-vue-next'
import { useChatScrollFollow } from '@/composables/useChatScrollFollow'
import { getMessageList } from '@/api/chat'
import { useStream } from '@/api/chat/streame'
import { isAwaitingInput, useChatStreamHandler, type ChatMessage } from '@/composables/useChatStreamHandler'
import { isCollaborationTaskActive, type CollaborationTaskView } from '@/utils/collaborationDisplay'
const AgentStreamDisplay = defineAsyncComponent(() => import('./AgentStreamDisplay.vue'))
const props = defineProps<{ task: CollaborationTaskView; active?: boolean }>()
const { t } = useI18n()
const messages = reactive<ChatMessage[]>([])
const latestAssignment = computed(() => [...messages].reverse().find(message => message.role === 'user'))
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
.child-assignment {
  margin: 0 0 20px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
}
.child-assignment summary {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 46px;
  padding: 10px 12px;
  box-sizing: border-box;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  line-height: 1.5;
  list-style: none;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;
}
.child-assignment summary::-webkit-details-marker { display: none; }
.child-assignment summary:hover { background: var(--td-bg-color-container-hover); }
.child-assignment summary:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
.child-assignment-label { display: inline-flex; align-items: center; gap: 8px; font-weight: 500; }
.child-assignment-icon { flex-shrink: 0; color: var(--td-text-color-secondary); }
.child-assignment-chevron { margin-left: auto; flex-shrink: 0; color: var(--td-text-color-secondary); transition: transform var(--app-motion-fast) ease; }
.child-assignment[open] .child-assignment-chevron { transform: rotate(90deg); }
.child-assignment-status { flex-shrink: 0; padding: 2px 7px; border-radius: var(--app-radius-xs); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: var(--app-text-md); line-height: 1.5; }
.child-assignment-status.running { color: var(--td-brand-color); background: var(--td-brand-color-light); }
.child-assignment-status.waiting, .child-assignment-status.needs_attention { color: var(--td-warning-color); }
.child-assignment-status.failed { color: var(--td-error-color); }
.child-assignment-body {
  margin: 0;
  padding: 12px 16px;
  border-top: 1px solid var(--td-component-border);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 1.7;
}
@media (prefers-reduced-motion: reduce) {
  .child-assignment-chevron, .child-assignment summary { transition: none; }
}
.child-stream-error { color: var(--td-error-color); margin: 12px 0; }
</style>
