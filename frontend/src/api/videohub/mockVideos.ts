import type { Chapter, SubtitleCue, VideoCategory, VideoData } from '@/types/videohub'

const sources = [
  'BigBuckBunny.mp4',
  'ElephantsDream.mp4',
  'ForBiggerBlazes.mp4',
  'ForBiggerEscapes.mp4',
  'Sintel.mp4',
  'TearsOfSteel.mp4',
]

const posters = [
  'photo-1618005182384-a83a8bd57fbe',
  'photo-1506744038136-46273834b3fb',
  'photo-1516321318423-f06f85e504b3',
  'photo-1497366754035-f200968a6e72',
  'photo-1552664730-d307ca884978',
  'photo-1521737711867-e3b97375f902',
]

const definitions: Array<{
  title: string
  category: VideoCategory
  categoryName: string
  duration: string
  durationSeconds: number
  createdAt: string
  overview: string
}> = [
  { title: '从第一性原理思考问题', category: 'training', categoryName: '培训课程', duration: '42:18', durationSeconds: 2538, createdAt: '2026-08-18 14:30', overview: '从问题拆解到假设验证，建立可复用的第一性原理分析框架。' },
  { title: 'AI 大模型的技术演进与未来', category: 'training', categoryName: '技术前沿', duration: '35:42', durationSeconds: 2142, createdAt: '2026-08-16 09:15', overview: '回顾大模型技术路线，并讨论企业应用中的关键机会。' },
  { title: '大模型训练的边界', category: 'salon', categoryName: '技术沙龙', duration: '48:05', durationSeconds: 2885, createdAt: '2026-08-12 16:20', overview: '解析模型规模、数据质量与计算预算之间的约束关系。' },
  { title: '高质量数据集构建', category: 'training', categoryName: '培训课程', duration: '28:16', durationSeconds: 1696, createdAt: '2026-08-08 10:00', overview: '介绍数据采集、清洗、标注和质量评估的完整流程。' },
  { title: 'AI Native 产品方法论', category: 'interview', categoryName: '人物访谈', duration: '31:24', durationSeconds: 1884, createdAt: '2026-08-03 18:40', overview: '围绕用户价值、工作流重构与产品壁垒展开实战讨论。' },
  { title: '组织知识如何持续生长', category: 'general', categoryName: '通用分享', duration: '24:38', durationSeconds: 1478, createdAt: '2026-07-29 11:05', overview: '探索视频、文档和日常问答如何沉淀为组织知识网络。' },
]

function formatTime(seconds: number) {
  const minutes = Math.floor(seconds / 60)
  return `${String(minutes).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
}

function makeChapters(videoIndex: number, duration: number): Chapter[] {
  const titles = [
    ['背景与问题定义', '核心原理拆解', '实践方法与案例'],
    ['技术演进脉络', '关键能力突破', '未来趋势判断'],
    ['规模扩展规律', '数据与算力边界', '工程决策框架'],
    ['数据目标定义', '清洗与标注流程', '质量评估闭环'],
    ['AI Native 定位', '工作流重构', '产品壁垒构建'],
    ['知识沉淀机制', '关联与检索', '持续运营方法'],
  ][videoIndex]
  const chapterLength = Math.floor(duration / 3)

  return titles.map((title, index) => {
    const start = index * chapterLength
    const end = index === 2 ? duration : (index + 1) * chapterLength
    return {
      id: `v-${videoIndex + 1}-chapter-${index + 1}`,
      chapter_index: String(index + 1).padStart(2, '0'),
      chapter_title: title,
      start_time: formatTime(start),
      start_seconds: start,
      end_time: formatTime(end),
      end_seconds: end,
      chapter_summary: `本章围绕“${title}”展开，梳理关键观点、判断依据与可执行方法。`,
      knowledge_points: [
        {
          id: `v-${videoIndex + 1}-kp-${index + 1}-1`,
          title: `${title}：关键概念`,
          timestamp: formatTime(start + 20),
          seconds: start + 20,
          transcriptSnippet: `讲者从实际场景出发解释了${title}的核心概念。`,
        },
        {
          id: `v-${videoIndex + 1}-kp-${index + 1}-2`,
          title: `${title}：行动建议`,
          timestamp: formatTime(Math.min(start + 90, end - 1)),
          seconds: Math.min(start + 90, end - 1),
          transcriptSnippet: `将本章方法转化为可验证、可复盘的行动步骤。`,
        },
      ],
    }
  })
}

function makeSubtitles(videoIndex: number): SubtitleCue[] {
  const subject = definitions[videoIndex].title
  return [
    { start_seconds: 0, end_seconds: 8, text: `欢迎观看《${subject}》。` },
    { start_seconds: 8, end_seconds: 18, text: '接下来我们会先明确问题，再逐步拆解关键概念。' },
    { start_seconds: 18, end_seconds: 30, text: '请带着自己的实际场景思考这些方法如何落地。' },
    { start_seconds: 30, end_seconds: 45, text: '你可以点击下方章节和知识点快速定位内容。' },
  ]
}

export const MOCK_VIDEOS: VideoData[] = definitions.map((item, index) => ({
  id: `v-${String(index + 1).padStart(2, '0')}`,
  title: item.title,
  category: item.category,
  categoryName: item.categoryName,
  duration: item.duration,
  durationSeconds: item.durationSeconds,
  created_at: item.createdAt,
  video_url: `https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/${sources[index]}`,
  poster_url: `https://images.unsplash.com/${posters[index]}?q=80&w=1200&auto=format&fit=crop`,
  overview: item.overview,
  chapters: makeChapters(index, item.durationSeconds),
  subtitles: makeSubtitles(index),
}))
