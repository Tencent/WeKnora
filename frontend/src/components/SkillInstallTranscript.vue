<template>
  <teleport to="body">
    <transition name="skill-transcript">
      <section v-if="visible" class="skill-transcript" role="dialog" :aria-label="title">
        <header class="skill-transcript__head">
          <div class="skill-transcript__title" :title="title">{{ title }}</div>
          <t-button variant="text" shape="square" size="small" @click="close">
            <template #icon><t-icon name="close" /></template>
          </t-button>
        </header>

        <div ref="scrollRef" class="skill-transcript__body">
          <t-loading v-if="loading" size="small" />
          <p v-else-if="messages.length === 0" class="skill-transcript__empty">
            {{ $t('settings.sandbox.skillTranscriptEmpty') }}
          </p>
          <template v-else>
            <div v-for="(msg, index) in messages" :key="msg.id || index" class="skill-transcript__turn">
              <pre v-if="msg.role === 'user'" class="skill-transcript__prompt">{{ msg.content }}</pre>
              <AgentStreamDisplay
                v-else
                :session="msg"
                :session-id="sessionId"
                :user-query="''"
              />
            </div>
          </template>
        </div>
      </section>
    </transition>
  </teleport>
</template>

<script setup lang="ts">
import { nextTick, reactive, ref, watch } from 'vue'
import { useChatStreamHandler } from '@/composables/useChatStreamHandler'
import { useStream } from '@/api/chat/streame'
import { getMessageList } from '@/api/chat'
import AgentStreamDisplay from '@/views/chat/components/AgentStreamDisplay.vue'

const props = defineProps<{
  visible: boolean
  sessionId: string
  messageId: string
  title: string
  // live selects the source. A running install is tailed through
  // continue-stream, which replays the whole event log before following it, so
  // it covers history and live in one call. A finished install may have
  // outlived the event log's TTL, so it is read from the durable messages.
  live: boolean
}>()

const emit = defineEmits<{ (e: 'update:visible', value: boolean): void }>()

const messages = reactive<any[]>([])
const loading = ref(false)
const isReplying = ref(false)
const currentAssistantMessageId = ref('')
const fullContent = ref('')
const scrollRef = ref<HTMLElement | null>(null)

// onChunk registers the handler; startStream takes only the request params
// (see frontend/src/api/chat/streame.ts:206-234).
const { startStream, stopStream, onChunk } = useStream()

function scrollToBottom() {
  void nextTick(() => {
    const el = scrollRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

const { handleMsgList, processStreamChunk } = useChatStreamHandler({
  messagesList: messages,
  loading,
  isReplying,
  currentAssistantMessageId,
  fullContent,
  isAgentStreamSession: () => true,
  scrollToBottom,
})

onChunk(processStreamChunk)

function close() {
  stopStream()
  emit('update:visible', false)
}

async function loadPersisted() {
  const res: any = await getMessageList({
    session_id: props.sessionId,
    limit: 100,
    created_at: '',
  })
  handleMsgList(res?.data || [])
  scrollToBottom()
}

async function open() {
  messages.splice(0, messages.length)
  loading.value = true
  try {
    if (props.live) {
      await startStream({
        session_id: props.sessionId,
        query: props.messageId,
        method: 'GET',
        url: '/api/v1/sessions/continue-stream',
      })
    } else {
      await loadPersisted()
    }
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.visible, props.sessionId, props.messageId] as const,
  ([isVisible]) => {
    stopStream()
    if (isVisible && props.sessionId && props.messageId) void open()
  },
  { immediate: true },
)

// A live run that ends while the window is open leaves the stream closed but
// the window showing the last frame. Re-reading the durable rows keeps what is
// on screen consistent with what a reopen would show.
watch(
  () => props.live,
  (isLive, wasLive) => {
    if (wasLive && !isLive && props.visible) {
      stopStream()
      void loadPersisted()
    }
  },
)
</script>

<style scoped lang="less">
.skill-transcript {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 3500;
  display: flex;
  flex-direction: column;
  width: 380px;
  max-width: calc(100vw - 32px);
  height: 500px;
  max-height: calc(100vh - 88px);
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 12px;
  box-shadow: 0 12px 32px rgb(0 0 0 / 16%);
  overflow: hidden;
}

.skill-transcript__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 8px 10px 16px;
  border-bottom: 1px solid var(--td-component-stroke, #e7e7e7);
}

.skill-transcript__title {
  overflow: hidden;
  font-size: 14px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-transcript__body {
  flex: 1;
  padding: 12px 16px;
  overflow-y: auto;
}

.skill-transcript__turn + .skill-transcript__turn {
  margin-top: 12px;
}

.skill-transcript__prompt {
  margin: 0;
  padding: 8px 10px;
  color: var(--td-text-color-secondary, #666);
  font-size: 12px;
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  border-radius: 8px;
  white-space: pre-wrap;
  word-break: break-word;
}

.skill-transcript__empty {
  margin: 24px 0;
  color: var(--td-text-color-placeholder, #999);
  font-size: 13px;
  text-align: center;
}

.skill-transcript-enter-active,
.skill-transcript-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.skill-transcript-enter-from,
.skill-transcript-leave-to {
  opacity: 0;
  transform: translateY(12px);
}
</style>
