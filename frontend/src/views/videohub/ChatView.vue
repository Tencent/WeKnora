<template>
  <main class="chat-page">
    <section v-if="!activeSession" class="chat-landing">
      <div class="chat-greeting"><span class="chat-avatar">AI</span><div><h1>Hi, Alice, 您可以向知识库提问</h1><p>从组织的视频知识中寻找答案与依据</p></div></div>
      <form class="chat-composer" @submit.prevent="startSession">
        <textarea v-model="question" rows="4" placeholder="输入你的问题，Enter 发送，Shift + Enter 换行" @keydown.enter.exact.prevent="startSession" />
        <footer><div><t-button variant="text" shape="square" type="button" aria-label="添加附件"><t-icon name="attach" /></t-button><t-button variant="text" shape="square" type="button" aria-label="语音输入"><t-icon name="microphone" /></t-button></div><t-button type="submit" shape="circle" :disabled="!question.trim()"><t-icon name="send" /></t-button></footer>
      </form>
      <section class="recent"><h2>最近对话</h2><div v-if="loadingSessions" class="recent__state"><t-loading size="small" /></div><t-empty v-else-if="sessions.length === 0" description="暂无历史对话" />
        <button v-for="session in sessions" v-else :key="session.id" type="button" class="session-card" @click="openSession(session)">
          <span v-if="session.scope === 'video'" class="session-card__cover"><img v-if="session.videoCoverUrl" :src="session.videoCoverUrl" alt="" /><t-icon v-else name="play-circle" /></span>
          <span v-else class="session-card__icon"><t-icon name="chat" /></span>
          <span class="session-card__body"><strong>{{ session.title }}</strong><small>{{ session.scope === 'video' ? session.videoTitle || '指定视频问答' : '全局视频问答' }} · {{ session.time }}</small></span>
        </button>
      </section>
    </section>
    <section v-else class="conversation">
      <header><t-button variant="text" @click="back"><t-icon name="chevron-left" /> 返回</t-button><div><strong>{{ activeSession.title }}</strong><span>{{ activeSession.scope === 'video' ? activeSession.videoTitle || '指定视频问答' : '全局视频问答' }}</span></div></header>
      <div ref="messageArea" class="conversation__messages">
        <article v-for="message in activeSession.messages" :key="message.id" :class="['message', `message--${message.sender}`]">
          <div v-if="message.sender === 'assistant'" class="message__identity"><span>AI</span><strong>WeKnora AI</strong></div>
          <div class="message__bubble">
            <AgentStreamDisplay
              v-if="message.sender === 'assistant'"
              :session="toNativeAgentSession(message)"
              :session-id="activeSession.id"
              :user-query="lastUserQuery"
              :hydrate-protected-images="false"
            />
            <p v-else>{{ message.text }}</p>
            <button v-if="message.relatedVideoId" type="button" class="video-reference" @click="openReference(message)"><span class="video-reference__poster"><t-icon name="play-circle" /></span><span><strong>{{ message.relatedVideoTitle }}</strong><small>点击跳转至 {{ formatTime(message.relatedTime || 0) }} 位置</small></span><t-icon name="jump" /></button>
            <button v-if="message.sender === 'assistant'" class="copy" type="button" @click="copyMessage(message)"><t-icon :name="copiedId === message.id ? 'check' : 'file-copy'" /> {{ copiedId === message.id ? '已复制' : '复制' }}</button>
          </div>
        </article>
        <div v-if="isGenerating && !streamingAssistantVisible" class="generating"><t-loading size="small" /> AI 正在整合视频知识回答中...</div>
      </div>
      <form class="conversation-composer" @submit.prevent="continueSession">
        <textarea v-model="followUp" rows="2" placeholder="继续追问" :disabled="isGenerating" @keydown.enter.exact.prevent="continueSession" />
        <t-button type="submit" shape="circle" :disabled="isGenerating || !followUp.trim()"><t-icon name="send" /></t-button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { createChatTurn, fetchSessions, loadChatSession } from '@/api/videohub/chat'
import type { StreamingChatMessage } from '@/api/videohub/chat'
import type { ChatMessage, ChatSession } from '@/types/videohub'
import AgentStreamDisplay from '@/views/chat/components/AgentStreamDisplay.vue'

const router = useRouter()
const question = ref(''), followUp = ref(''), sessions = ref<ChatSession[]>([]), activeSession = ref<ChatSession | null>(null)
const loadingSessions = ref(true), isGenerating = ref(false), copiedId = ref(''), messageArea = ref<HTMLElement | null>(null)
const streamingAssistantId = ref('')
const lastUserQuery = ref('')
let copyTimer: number | undefined
type RenderableChatMessage = ChatMessage & Partial<StreamingChatMessage>
const streamingAssistantVisible = computed(() => Boolean(streamingAssistantId.value && activeSession.value?.messages.some(message => {
  const item = message as RenderableChatMessage
  return item.id === streamingAssistantId.value && Boolean(item.text || item.activityText || item.agentEventStream?.length)
})))

async function scrollBottom() { await nextTick(); if (messageArea.value) messageArea.value.scrollTop = messageArea.value.scrollHeight }
function toNativeAgentSession(message: ChatMessage) {
  const item = message as RenderableChatMessage
  return {
    id: item.id,
    assistant_message_id: item.assistant_message_id || item.id,
    request_id: item.request_id || item.id,
    role: 'assistant',
    content: item.content || item.text,
    isAgentMode: true,
    is_completed: item.is_completed ?? true,
    agentEventStream: item.agentEventStream?.length
      ? item.agentEventStream
      : [{ type: 'answer', content: item.text, done: true }, { type: 'agent_complete', total_duration_ms: 0, total_steps: 0 }],
    knowledge_references: [],
  }
}
function updateStreamingMessage(messageId: string, message: StreamingChatMessage) {
  if (!activeSession.value) return
  const target = activeSession.value.messages.find(item => item.id === messageId) as RenderableChatMessage | undefined
  if (!target) return
  Object.assign(target, message, { id: messageId })
  void scrollBottom()
}
function pushStreamingPlaceholder(target: ChatSession) {
  const assistantId = `assistant-${Date.now()}`
  streamingAssistantId.value = assistantId
  target.messages.push({ id: assistantId, sender: 'assistant', text: '', timestamp: '刚刚' } as StreamingChatMessage)
  return assistantId
}

function materializePendingSession(pendingSession: ChatSession, createdSession: ChatSession) {
  const materialized = { ...createdSession, messages: pendingSession.messages }
  sessions.value = sessions.value.map(item => item.id === pendingSession.id ? materialized : item)
  if (activeSession.value?.id === pendingSession.id) activeSession.value = materialized
  return materialized
}
async function startSession() {
  const value = question.value.trim(); if (!value || isGenerating.value) return
  lastUserQuery.value = value
  question.value = ''
  const session: ChatSession = { id: `pending-${Date.now()}`, title: value.slice(0, 24), type: 'chat', time: '刚刚', messages: [{ id: `user-${Date.now()}`, sender: 'user', text: value, timestamp: '刚刚' }], scope: 'global' }
  const assistantId = pushStreamingPlaceholder(session)
  sessions.value.unshift(session); activeSession.value = session; isGenerating.value = true; await scrollBottom()
  try {
    const created = await createChatTurn(value, { globalMode: true, onSessionCreated: createdSession => materializePendingSession(session, createdSession), onStreamMessage: message => updateStreamingMessage(assistantId, message) })
    sessions.value = sessions.value.map(item => item.id === session.id || item.id === created.id ? created : item)
    activeSession.value = created
  } catch (error) {
    session.messages.push({ id: `error-${Date.now()}`, sender: 'assistant', text: error instanceof Error ? error.message : '问答生成失败，请稍后重试', timestamp: '刚刚' })
  } finally { isGenerating.value = false; streamingAssistantId.value = ''; await scrollBottom() }
}
async function continueSession() {
  const value = followUp.value.trim(); if (!value || isGenerating.value || !activeSession.value) return
  const target = activeSession.value
  lastUserQuery.value = value
  followUp.value = ''
  target.messages.push({ id: `user-${Date.now()}`, sender: 'user', text: value, timestamp: '刚刚' })
  const assistantId = pushStreamingPlaceholder(target)
  isGenerating.value = true; await scrollBottom()
  try {
    const updated = await createChatTurn(value, { globalMode: target.scope !== 'video', currentVideo: target.videoId ? { id: target.videoId, title: target.videoTitle || '指定视频', category: 'general', categoryName: '通用分享', duration: '', durationSeconds: 0, created_at: '', video_url: '', poster_url: target.videoCoverUrl, overview: '', chapters: [], subtitles: [] } : undefined, session: target, onStreamMessage: message => updateStreamingMessage(assistantId, message) })
    activeSession.value = updated
    sessions.value = [updated, ...sessions.value.filter(item => item.id !== updated.id)]
  } catch (error) {
    target.messages.push({ id: `error-${Date.now()}`, sender: 'assistant', text: error instanceof Error ? error.message : '问答生成失败，请稍后重试', timestamp: '刚刚' })
  } finally { isGenerating.value = false; streamingAssistantId.value = ''; await scrollBottom() }
}
async function openSession(session: ChatSession) {
  activeSession.value = session
  if (!session.messages.length) {
    try { activeSession.value = await loadChatSession(session) }
    catch (error) { MessagePlugin.error(error instanceof Error ? error.message : '会话加载失败') }
  }
  void scrollBottom()
}
function back() { activeSession.value = null; followUp.value = ''; isGenerating.value = false }
function formatTime(seconds: number) { const safe = Math.max(0, Math.floor(seconds)); return `${String(Math.floor(safe / 60)).padStart(2, '0')}:${String(safe % 60).padStart(2, '0')}` }
function openReference(message: ChatMessage) { if (message.relatedVideoId) router.push(`/platform/videos/${message.relatedVideoId}?t=${message.relatedTime || 0}`) }
async function copyMessage(message: ChatMessage) {
  try { await navigator.clipboard.writeText(message.text); copiedId.value = message.id; if (copyTimer) window.clearTimeout(copyTimer); copyTimer = window.setTimeout(() => { copiedId.value = '' }, 2000) }
  catch { MessagePlugin.warning('复制失败，请手动选择文本') }
}
onMounted(async () => { try { sessions.value = await fetchSessions() } finally { loadingSessions.value = false } })
</script>

<style scoped>
.chat-page { height: 100%; overflow-y: auto; padding: 48px 32px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); }.chat-landing { max-width: 640px; margin: 0 auto; }.chat-greeting { display: flex; align-items: center; gap: 16px; margin-bottom: 28px; }.chat-avatar, .message__identity span { display: grid; flex: 0 0 auto; width: 42px; height: 42px; place-items: center; border-radius: var(--td-radius-circle); background: var(--td-brand-color); color: var(--td-text-color-anti); font-weight: 600; }.chat-greeting h1 { margin: 0; font-size: 26px; font-weight: 400; }.chat-greeting p { margin: 6px 0 0; color: var(--td-text-color-secondary); }.chat-composer { overflow: hidden; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); box-shadow: var(--td-shadow-1); }.chat-composer:focus-within { border-color: var(--td-brand-color); }.chat-composer textarea { box-sizing: border-box; width: 100%; resize: none; padding: 18px; border: 0; outline: 0; background: transparent; color: var(--td-text-color-primary); font: inherit; line-height: 1.6; }.chat-composer footer { display: flex; align-items: center; justify-content: space-between; padding: 8px 12px 12px; }.recent { margin-top: 36px; }.recent h2 { font-size: 16px; font-weight: 500; }.recent__state { padding: 32px; text-align: center; }.session-card { display: flex; align-items: center; gap: 12px; width: 100%; margin-bottom: 8px; padding: 12px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-large); background: var(--td-bg-color-container); color: var(--td-text-color-primary); cursor: pointer; text-align: left; }.session-card:hover { border-color: var(--td-brand-color); box-shadow: var(--td-shadow-1); }.session-card__icon, .session-card__cover { display: grid; flex: 0 0 42px; width: 42px; height: 42px; place-items: center; overflow: hidden; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-brand-color); }.session-card__cover img { width: 100%; height: 100%; object-fit: cover; }.session-card__body { display: grid; gap: 3px; min-width: 0; }.session-card__body strong, .session-card__body small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.session-card small { color: var(--td-text-color-placeholder); }.conversation { display: grid; grid-template-rows: auto 1fr auto; max-width: 900px; min-height: calc(100vh - 150px); margin: 0 auto; }.conversation > header { display: flex; align-items: center; gap: 12px; padding-bottom: 16px; border-bottom: 1px solid var(--td-border-level-1-color); }.conversation > header div { display: grid; }.conversation > header span { color: var(--td-text-color-secondary); font-size: 12px; }.conversation__messages { overflow-y: auto; padding: 24px 4px; }.conversation-composer { display: grid; grid-template-columns: 1fr auto; gap: 10px; align-items: end; padding: 10px 12px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); box-shadow: var(--td-shadow-1); }.conversation-composer:focus-within { border-color: var(--td-brand-color); }.conversation-composer textarea { box-sizing: border-box; min-width: 0; resize: none; border: 0; outline: 0; background: transparent; color: var(--td-text-color-primary); font: inherit; line-height: 1.6; }.message { display: flex; margin-bottom: 20px; }.message--user { justify-content: flex-end; }.message--assistant { display: grid; grid-template-columns: 40px minmax(0, 1fr); column-gap: 10px; }.message__identity { display: grid; justify-items: center; gap: 6px; align-self: start; }.message__identity span { width: 30px; height: 30px; font-size: 11px; }.message__identity strong { color: var(--td-text-color-secondary); font-size: 12px; font-weight: 500; white-space: nowrap; }.message__bubble { min-width: 0; max-width: 72%; }.message--assistant .message__bubble { width: 100%; max-width: none; }.message__bubble > p { margin: 0; padding: 11px 14px; border-radius: var(--td-radius-large); background: var(--td-bg-color-secondarycontainer); line-height: 1.7; white-space: pre-wrap; }.message--assistant .message__bubble > p { background: transparent; }.message--assistant :deep(.agent-stream-display) { width: 100%; }.message--assistant :deep(.t-image-viewer__trigger--hover:empty) { display: none; }.video-reference { display: grid; grid-template-columns: 56px 1fr auto; align-items: center; gap: 10px; width: 100%; margin-top: 10px; padding: 8px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-large); background: var(--td-bg-color-container); color: var(--td-text-color-primary); cursor: pointer; text-align: left; }.video-reference:hover { border-color: var(--td-brand-color); }.video-reference__poster { display: grid; height: 42px; place-items: center; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-brand-color); }.video-reference span:nth-child(2) { display: grid; gap: 4px; }.video-reference small { color: var(--td-text-color-secondary); }.copy { margin-top: 6px; padding: 3px 6px; border: 0; background: transparent; color: var(--td-text-color-secondary); cursor: pointer; }.generating { display: flex; align-items: center; gap: 8px; color: var(--td-text-color-secondary); }@media (max-width: 760px) { .chat-page { padding: 28px 18px; }.message__bubble { max-width: 88%; }.message--assistant .message__bubble { max-width: none; }.message__identity strong { display: none; } }
</style>
