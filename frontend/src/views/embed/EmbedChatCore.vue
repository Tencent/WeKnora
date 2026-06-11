<template>
  <div class="embed-chat">
    <div ref="scrollContainer" class="embed-chat__scroll" @scroll="handleScroll">
      <div class="embed-chat__messages">
        <div v-if="historyLoading && messagesList.length === 0" class="msg-skeleton-list">
          <div class="msg-skeleton msg-skeleton-user"><div class="sk-line sk-line--short" /></div>
          <div class="msg-skeleton msg-skeleton-bot">
            <div class="sk-line" />
            <div class="sk-line" />
            <div class="sk-line sk-line--medium" />
          </div>
        </div>

        <div
          v-for="(session, index) in messagesList"
          :key="(session.id as string) || `${session.role}-${session.created_at}-${index}`"
          class="msg-item-wrapper"
        >
          <div v-if="session.role === 'user'">
            <EmbedUserMessage
              :content="String(session.content || '')"
              :mentioned_items="session.mentioned_items"
              :images="session.images"
              :attachments="session.attachments"
              :embeddedMode="true"
            />
          </div>
          <div v-if="session.role === 'assistant' && shouldRenderAssistantMessage(session)">
            <EmbedBotMessage
              :content="String(session.content || '')"
              :session="session"
              :session-id="sessionId"
              :user-query="getUserQuery(index)"
              :embeddedMode="true"
            />
          </div>
        </div>

        <div v-if="loading" class="embed-chat__typing">
          <div class="loading-typing">
            <span></span>
            <span></span>
            <span></span>
          </div>
        </div>
      </div>
    </div>

    <transition name="scroll-btn-fade">
      <div v-show="userHasScrolledUp" class="scroll-to-bottom-btn" @click="onClickScrollToBottom" aria-label="scroll to bottom">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M6 9l6 6 6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </div>
    </transition>

    <div class="embed-chat__input">
      <EmbedInputField
        :isReplying="isReplying"
        @send-msg="sendMsg"
        @stop-generation="handleStopGeneration"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, toRef, watch } from 'vue'
import EmbedInputField from '@/components/EmbedInputField.vue'
import EmbedBotMessage from '@/views/embed/EmbedBotMessage.vue'
import EmbedUserMessage from '@/views/embed/EmbedUserMessage.vue'
import { useEmbedChatSession } from '@/composables/useEmbedChatSession'

const props = defineProps<{
  sessionId: string
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  hostContext?: Record<string, unknown>
}>()

const emit = defineEmits<{
  (e: 'messages-change', hasMessages: boolean): void
}>()

const sessionIdRef = toRef(props, 'sessionId')
const hostContextRef = ref<Record<string, unknown>>(props.hostContext || {})

watch(() => props.hostContext, (ctx) => {
  hostContextRef.value = ctx || {}
}, { deep: true })

const {
  messagesList,
  loading,
  isReplying,
  historyLoading,
  scrollContainer,
  userHasScrolledUp,
  shouldRenderAssistantMessage,
  getUserQuery,
  handleScroll,
  scrollToBottom,
  onClickScrollToBottom,
  sendMsg,
  handleStopGeneration,
} = useEmbedChatSession({
  sessionId: sessionIdRef,
  channelId: props.channelId,
  token: props.token,
  agentId: props.agentId,
  kbIds: props.kbIds,
  hostContext: hostContextRef,
  onMessagesChange: (has) => emit('messages-change', has),
})
</script>

<style scoped lang="less">
.embed-chat {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  width: 100%;
  position: relative;
}

.embed-chat__scroll {
  flex: 1;
  min-height: 0;
  width: 100%;
  overflow-y: auto;
}

.embed-chat__messages {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 800px;
  margin: 0 auto;
  width: 100%;
  padding: 12px 16px 0;
  box-sizing: border-box;

  .msg-item-wrapper {
    contain: layout style;
  }
}

.embed-chat__typing {
  height: 41px;
  display: flex;
  align-items: center;
  padding-left: 4px;
}

.embed-chat__input {
  flex-shrink: 0;
  padding: 12px 16px 16px;
  box-sizing: border-box;
}

.msg-skeleton-list {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 8px 0;
}

.msg-skeleton-user {
  display: flex;
  justify-content: flex-end;
}

.msg-skeleton-bot {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-left: 4px;
}

.sk-line {
  height: 14px;
  border-radius: 6px;
  background: linear-gradient(90deg, #f0f0f0 25%, #e6e6e6 50%, #f0f0f0 75%);
  background-size: 200% 100%;
  animation: sk-shimmer 1.2s ease-in-out infinite;
}

.sk-line--short { width: 45%; height: 36px; }
.sk-line--medium { width: 60%; }

@keyframes sk-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

.loading-typing {
  display: flex;
  align-items: center;
  gap: 4px;

  span {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--embed-primary, #0052d9);
    animation: typingBounce 1.4s ease-in-out infinite;

    &:nth-child(1) { animation-delay: 0s; }
    &:nth-child(2) { animation-delay: 0.2s; }
    &:nth-child(3) { animation-delay: 0.4s; }
  }
}

@keyframes typingBounce {
  0%, 60%, 100% { transform: translateY(0); }
  30% { transform: translateY(-8px); }
}

.scroll-to-bottom-btn {
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  bottom: 100px;
  z-index: 10;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--td-text-color-secondary);
}

.scroll-btn-fade-enter-active,
.scroll-btn-fade-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.scroll-btn-fade-enter-from,
.scroll-btn-fade-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(8px);
}
</style>
