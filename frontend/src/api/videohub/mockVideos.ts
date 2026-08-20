import type { Chapter, CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, RelationType, SubtitleCue, SummarySection, VideoCategory, VideoData } from '@/types/videohub'

const summaryTitles: Record<VideoCategory, readonly string[]> = {
  interview: ['一、人物背景', '二、经历与决策', '三、核心观点', '四、原则与思维模型', '五、案例与证据', '六、反思与边界'],
  training: ['一、目标与受众', '二、知识地图', '三、核心概念', '四、方法与步骤', '五、示例与异常', '六、练习与应用'],
  salon: ['一、活动与参与者', '二、议题与观点', '三、观点交锋', '四、案例与问答', '五、共识与分歧', '六、探索方向'],
  general: ['一、定位与问题', '二、主张与论证', '三、证据与案例', '四、限定与反方', '五、影响与建议'],
}

const knowledgeLabels: Record<KnowledgeType, string> = {
  entity: '关键人物与组织', concept: '核心概念', case: '实践案例', method: '落地方法', insight: '关键洞察',
}

const knowledgeDistributions: KnowledgeType[][] = [
  ['entity', 'concept', 'case'],
  ['entity', 'concept', 'case', 'method', 'insight'],
  ['entity', 'method'],
  [],
  ['concept', 'insight'],
  ['case', 'method'],
]

const relationTypes: RelationType[] = ['相同', '相似', '补充', '对比', '延伸']

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

function makeSummary(videoIndex: number, category: VideoCategory, duration: number): SummarySection[] {
  return summaryTitles[category].map((title, index) => {
    const seconds = Math.min(Math.round(((index + 1) * duration) / (summaryTitles[category].length + 1)), duration - 1)
    return {
      id: `v-${videoIndex + 1}-summary-${index + 1}`,
      title,
      content: `${definitions[videoIndex].overview}\n\n本节从“${title.replace(/^.+、/, '')}”出发，提炼关键判断、论证过程与可执行启示。`,
      evidenceTimestamp: formatTime(seconds),
      evidenceSeconds: seconds,
      transcriptSnippet: `讲者围绕${title.replace(/^.+、/, '')}展开说明，并结合《${definitions[videoIndex].title}》的主题给出具体依据。`,
    }
  })
}

function makeRelatedKnowledge(videoIndex: number, duration: number) {
  const types = knowledgeDistributions[videoIndex]
  if (types.length === 0) return {}

  const anchors: CurrentKnowledgeAnchor[] = []
  const crossVideoItems: CrossVideoKnowledgeItem[] = []
  types.forEach((knowledgeType, typeIndex) => {
    const anchorCount = videoIndex === 1 ? 2 : 1
    for (let anchorIndex = 0; anchorIndex < anchorCount; anchorIndex += 1) {
      const anchorId = `v-${videoIndex + 1}-anchor-${knowledgeType}-${anchorIndex + 1}`
      const seconds = Math.min(Math.round(duration * ((typeIndex + anchorIndex + 1) / (types.length + anchorCount + 1))), duration - 1)
      anchors.push({
        id: anchorId,
        knowledge_type: knowledgeType,
        content: `${knowledgeLabels[knowledgeType]}：${definitions[videoIndex].title}中的${anchorIndex === 0 ? '核心判断' : '延伸结论'}`,
        timestamp: formatTime(seconds),
        seconds,
        related_count: 2,
      })
      for (let relationIndex = 0; relationIndex < 2; relationIndex += 1) {
        let targetIndex = (videoIndex + typeIndex + relationIndex + 1) % definitions.length
        if (targetIndex === videoIndex) targetIndex = (targetIndex + 1) % definitions.length
        const targetSeconds = Math.min(90 + typeIndex * 75 + relationIndex * 45, definitions[targetIndex].durationSeconds - 1)
        crossVideoItems.push({
          id: `${anchorId}-relation-${relationIndex + 1}`,
          anchorId,
          knowledge_type: knowledgeType,
          relation_type: relationTypes[(typeIndex + relationIndex) % relationTypes.length],
          knowledge_content: `${definitions[targetIndex].title}对“${knowledgeLabels[knowledgeType]}”给出了另一组可验证的信息。`,
          timestamp: formatTime(targetSeconds),
          seconds: targetSeconds,
          video_id: `v-${String(targetIndex + 1).padStart(2, '0')}`,
          video_title: definitions[targetIndex].title,
          video_category: definitions[targetIndex].category,
          relation_description: `该内容与当前视频的${knowledgeLabels[knowledgeType]}形成交叉印证，可用于补充理解和比较。`,
        })
      }
    }
  })

  const relatedVideoCount = new Set(crossVideoItems.map(item => item.video_id)).size
  const overview: RelationOverview = {
    relation_overview: `围绕${types.map(type => knowledgeLabels[type]).join('、')}建立跨视频关联。`,
    related_video_count: relatedVideoCount,
    relation_count: crossVideoItems.length,
    top_topics: types.map(type => knowledgeLabels[type]),
  }
  return { relationOverview: overview, currentAnchors: anchors, crossVideoItems }
}

export const MOCK_VIDEOS: VideoData[] = definitions.map((item, index) => {
  const summary = makeSummary(index, item.category, item.durationSeconds)
  const typedSummary = item.category === 'interview'
    ? { interviewSummary: summary }
    : item.category === 'training'
      ? { trainingSummary: summary }
      : item.category === 'salon'
        ? { salonSummary: summary }
        : { summarySections: summary }

  return {
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
    ...typedSummary,
    ...makeRelatedKnowledge(index, item.durationSeconds),
  }
})
