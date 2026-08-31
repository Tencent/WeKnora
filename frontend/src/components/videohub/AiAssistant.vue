<template>
  <div class="assistant-shell">
    <section v-if="expanded" class="assistant-drawer">
      <header><div><strong>AI Assistant</strong></div><t-button variant="text" shape="square" aria-label="收起" @click="expanded = false"><t-icon name="chevron-down" /></t-button></header>
      <div ref="messageArea" class="assistant-messages">
        <div v-for="message in messages" :key="message.id" :class="['assistant-message', `assistant-message--${message.sender}`]">
          <p><template v-for="(part, index) in splitTimestamps(message.text, message.evidenceLinks)" :key="index"><button v-if="part.seconds !== undefined" class="timestamp" type="button" @click="selectTimestamp(part)">{{ part.text }}</button><template v-else>{{ part.text }}</template></template></p>
        </div>
        <div v-if="isGenerating" class="assistant-loading"><t-loading size="small" /> 正在整合视频知识...</div>
      </div>
      <div class="assistant-suggestions"><button v-for="item in suggestions" :key="item" type="button" @click="send(item)">{{ item }}</button></div>
    </section>
    <form class="assistant-bar" @submit.prevent="send(input)">
      <input v-model="input" :disabled="isGenerating" :placeholder="globalMode ? '向 AI 提问知识库全部视频内容' : '向 AI 提问当前视频内容'" @focus="expanded = true" /><t-button type="submit" shape="circle" :disabled="isGenerating || !input.trim()"><t-icon name="send" /></t-button>
    </form>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { createChatTurn } from '@/api/videohub/chat'
import type { ChatMessage, ChatSession, VideoData } from '@/types/videohub'

type TimestampPart = { text: string; seconds?: number; videoId?: string }

const props = withDefaults(defineProps<{ currentVideo: VideoData; currentTime?: number; externalQuery?: string; globalMode?: boolean }>(), { currentTime: 0, externalQuery: '', globalMode: false })
const emit = defineEmits<{ seek: [seconds: number]; navigate: [videoId: string, seconds: number] }>()
const expanded = ref(false), input = ref(''), isGenerating = ref(false), messageArea = ref<HTMLElement | null>(null)
const messages = ref<ChatMessage[]>([])
const activeSession = ref<ChatSession | null>(null)
const globalSuggestions = ['帮我总结一下全部视频的核心观点', '最近上传了哪些重要视频？', '帮我找关于培训内容的视频']
const singleSuggestions = ['总结这段视频的核心观点', '有哪些值得记录的知识点？', '给出三个可执行建议']
const suggestions = props.globalMode ? globalSuggestions : singleSuggestions
let consumedExternalQuery = ''

function welcome(video: VideoData, global: boolean): ChatMessage {
  return {
    id: `welcome-${video.id}`,
    sender: 'assistant',
    text: global ? '你好，我可以基于知识库全部视频回答问题，回答中会标注来自哪条视频的哪个时间点。' : `你好，我可以基于《${video.title}》回答问题。`,
    timestamp: ''
  }
}
messages.value = [welcome(props.currentVideo, props.globalMode)]
function splitTimestamps(text: string, evidenceLinks: ChatMessage['evidenceLinks'] = []): TimestampPart[] {
  return text.split(/(\[\d{2}:\d{2}\])/g).filter(Boolean).map(part => {
    const match = part.match(/^\[(\d{2}):(\d{2})\]$/)
    if (!match) return { text: part }
    const seconds = Number(match[1]) * 60 + Number(match[2])
    const evidence = evidenceLinks.find(item => item.seconds === seconds || item.timestamp === `${match[1]}:${match[2]}`)
    return { text: part, seconds, videoId: evidence?.videoId }
  })
}
function selectTimestamp(part: TimestampPart) {
  if (part.seconds === undefined) return
  // 全局模式下导航由 evidenceLinks 里的 videoId 决定，此处只走 navigate 事件 + currentVideo
  if (props.globalMode) {
    if (part.videoId) emit('navigate', part.videoId, part.seconds)
  } else {
    emit('seek', part.seconds)
  }
}
async function scrollBottom() { await nextTick(); if (messageArea.value) messageArea.value.scrollTop = messageArea.value.scrollHeight }
async function send(value: string) {
  const question = value.trim(); if (!question || isGenerating.value) return
  expanded.value = true; input.value = ''
  messages.value.push({ id: `user-${Date.now()}`, sender: 'user', text: question, timestamp: '' }); isGenerating.value = true; await scrollBottom()
  try {
    const session = await createChatTurn(question, { currentVideo: props.currentVideo, currentTime: props.currentTime, globalMode: props.globalMode, session: activeSession.value || undefined })
    activeSession.value = session
    messages.value = [welcome(props.currentVideo, props.globalMode), ...session.messages]
  }
  catch (error) {
    const text = error instanceof Error ? error.message : '问答生成失败，请稍后重试'
    messages.value.push({ id: `error-${Date.now()}`, sender: 'assistant', text, timestamp: '' })
    MessagePlugin.error(text)
  } finally { isGenerating.value = false; await scrollBottom() }
}
watch([() => props.currentVideo.id, () => props.globalMode], () => { activeSession.value = null; messages.value = [welcome(props.currentVideo, props.globalMode)]; input.value = ''; isGenerating.value = false })
watch(() => props.externalQuery, value => { if (value && value !== consumedExternalQuery) { consumedExternalQuery = value; void send(value) } }, { immediate: true })
</script>

<style scoped>
.assistant-shell { position: fixed; z-index: 20; right: 0; bottom: 0; left: 260px; pointer-events: none; }.assistant-bar, .assistant-drawer { width: min(760px, calc(100% - 32px)); margin: 0 auto 16px; border: var(--border-width-hairline, .5px) solid var(--td-border-level-1-color); background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent); box-shadow: var(--shadow-popup, 0 8px 24px color-mix(in srgb, var(--td-text-color-primary) 10%, transparent)); backdrop-filter: blur(20px) saturate(180%); pointer-events: auto; }.assistant-bar { display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 16px; border-radius: var(--td-radius-extraLarge); }.assistant-bar input { flex: 1; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--td-text-color-primary); font: inherit; }.assistant-drawer { overflow: hidden; max-height: 420px; margin-bottom: 8px; border-radius: var(--td-radius-extraLarge); }.assistant-drawer header { display: flex; align-items: center; justify-content: space-between; padding: 12px 16px; border-bottom: 1px solid var(--td-border-level-1-color); }.assistant-drawer header div { display: grid; gap: 2px; }.assistant-drawer header span { color: var(--td-text-color-secondary); font-size: 12px; }.assistant-messages { overflow-y: auto; max-height: 260px; padding: 14px 16px; }.assistant-message { margin-bottom: 12px; }.assistant-message--user { text-align: right; }.assistant-message strong { color: var(--td-brand-color); font-size: 12px; }.assistant-message p { display: inline-block; max-width: 86%; margin: 4px 0 0; padding: 9px 12px; border-radius: var(--td-radius-large); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary); line-height: 1.6; white-space: pre-wrap; text-align: left; }.timestamp { padding: 1px 6px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-round); background: var(--td-bg-color-container); color: var(--td-brand-color); cursor: pointer; }.timestamp:hover { border-color: var(--td-brand-color); }.assistant-loading { color: var(--td-text-color-secondary); font-size: 13px; }.assistant-suggestions { display: flex; gap: 6px; overflow-x: auto; padding: 0 16px 12px; }.assistant-suggestions button { padding: 6px 10px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-round); background: var(--td-bg-color-container); color: var(--td-text-color-secondary); cursor: pointer; white-space: nowrap; }.assistant-suggestions button:hover { border-color: var(--td-brand-color); color: var(--td-brand-color); }@media (max-width: 900px) { .assistant-shell { left: 0; }.assistant-bar, .assistant-drawer { width: calc(100% - 24px); } }
</style>
