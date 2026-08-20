import { MOCK_VIDEOS } from './mockVideos'
import type { ChatMessage, ChatSession, EvidenceLink, VideoData } from '@/types/videohub'

const wait = (milliseconds: number) => new Promise(resolve => window.setTimeout(resolve, milliseconds))
const messageId = () => `message-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
const now = () => new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })

function normalizeQuestion(question: string) {
  return question.replace(/[《》]/g, '').trim().toLocaleLowerCase()
}

function findMatchingVideo(question: string) {
  const normalized = normalizeQuestion(question)
  return MOCK_VIDEOS.find(video => normalized.includes(video.title.toLocaleLowerCase()))
}

function formatTime(seconds: number) {
  const safe = Math.max(0, Math.floor(seconds))
  return `${String(Math.floor(safe / 60)).padStart(2, '0')}:${String(safe % 60).padStart(2, '0')}`
}

function makeInitialMessage(text: string): ChatMessage {
  return { id: messageId(), sender: 'assistant', text, timestamp: '最近' }
}

export async function fetchSessions(): Promise<ChatSession[]> {
  await wait(160)
  const sessions: ChatSession[] = [
    { id: 'session-1', title: '第一性原理如何应用？', type: 'chat', time: '10 分钟前', messages: [makeInitialMessage('第一性原理强调先拆解假设，再从基本事实重新构建解决方案。')] },
    { id: 'session-2', title: '总结大模型技术演进', type: 'chat', time: '昨天', messages: [makeInitialMessage('视频从规模化训练、能力涌现和企业落地三个阶段梳理了技术演进。')] },
    { id: 'session-3', title: '如何构建高质量数据集？', type: 'doc', time: '3 天前', messages: [makeInitialMessage('建议从目标定义、采集清洗、标注规范和质量评估四步推进。')] },
  ]
  return sessions.map(session => ({ ...session, messages: session.messages.map(message => ({ ...message })) }))
}

export async function sendChatMessage(question: string): Promise<ChatMessage> {
  await wait(1100)
  const video = findMatchingVideo(question)
  if (!video) {
    return {
      id: messageId(), sender: 'assistant', timestamp: now(),
      text: '暂未在当前视频知识库中找到与这个问题直接匹配的内容。你可以补充视频标题或更具体的关键词后再试。',
    }
  }
  const point = video.chapters[0]?.knowledge_points[0]
  const relatedTime = point?.seconds ?? video.chapters[0]?.start_seconds ?? 0
  return {
    id: messageId(), sender: 'assistant', timestamp: now(),
    text: `根据《${video.title}》，可以从三个层面理解：\n\n1. 先明确问题和关键假设；\n2. 结合视频中的方法拆解影响因素；\n3. 用可验证的行动形成反馈闭环。`,
    relatedVideoId: video.id, relatedVideoTitle: video.title, relatedTime,
  }
}

export async function sendAssistantQuery(question: string, currentVideo: VideoData, currentTime: number): Promise<ChatMessage> {
  await wait(1000)
  const points = currentVideo.chapters.flatMap(chapter => chapter.knowledge_points).slice(0, 3)
  const fallbackSeconds = Math.min(Math.max(Math.floor(currentTime), 0), currentVideo.durationSeconds)
  const evidenceLinks: EvidenceLink[] = (points.length ? points : [{ title: '当前播放位置', timestamp: formatTime(fallbackSeconds), seconds: fallbackSeconds }])
    .map(point => ({ label: point.title, timestamp: point.timestamp, seconds: point.seconds }))
  const references = evidenceLinks.map(item => `[${item.timestamp}]`).join('、')
  return {
    id: messageId(), sender: 'assistant', timestamp: now(), evidenceLinks,
    text: `关于“${question}”，《${currentVideo.title}》中的 ${references} 提供了关键依据。建议先理解核心概念，再结合章节中的行动建议落地。`,
  }
}
