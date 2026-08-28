import type {
  Chapter,
  CrossVideoKnowledgeItem,
  CurrentKnowledgeAnchor,
  KnowledgeType,
  RelationOverview,
  RelationType,
  SummarySection,
  SubtitleCue,
} from '@/types/videohub'

interface MarkdownSection {
  title: string
  body: string
}

interface BackendAnchor {
  id: string
  title?: string
  type?: KnowledgeType
  related_video_ids?: string[]
  timestamp?: string
  seconds?: number
}

interface BackendCrossVideoItem extends BackendAnchor {
  anchor_id?: string
  relation_type?: RelationType
  relation_description?: string
  video_id?: string
  video_title?: string
  video_type?: string
}

export interface BackendRelatedKnowledgeResponse {
  anchors?: Partial<Record<KnowledgeType, BackendAnchor[]>> | BackendAnchor[]
  cross_video?: BackendCrossVideoItem[]
  overview?: RelationOverview | null
}

const KNOWLEDGE_TYPES: KnowledgeType[] = ['entity', 'concept', 'case', 'method', 'insight']
const RELATION_TYPES = new Set<RelationType>(['相同', '相似', '补充', '对比', '延伸'])

export function parseTimestamp(value: string): number {
  const parts = value.trim().split(':').map(Number)
  if ((parts.length !== 2 && parts.length !== 3) || parts.some(part => !Number.isFinite(part) || part < 0)) {
    throw new Error(`无效时间戳：${value}`)
  }
  if (parts[parts.length - 1] >= 60 || (parts.length === 3 && parts[1] >= 60)) {
    throw new Error(`无效时间戳：${value}`)
  }
  if (parts.length === 2) return parts[0] * 60 + parts[1]
  return parts[0] * 3600 + parts[1] * 60 + parts[2]
}

function formatTimestamp(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(whole / 3600)
  const minutes = Math.floor((whole % 3600) / 60)
  const remainder = whole % 60
  return hours > 0
    ? `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
    : `${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
}

function stripFrontmatter(content: string): string {
  return content.replace(/^\s*---\s*\n[\s\S]*?\n---\s*\n?/, '').trim()
}

function splitLevelTwoSections(content: string): MarkdownSection[] {
  const source = stripFrontmatter(content)
  const matches = Array.from(source.matchAll(/^##\s+(.+)$/gm))
  return matches.map((match, index) => ({
    title: match[1].trim(),
    body: source.slice((match.index || 0) + match[0].length, matches[index + 1]?.index ?? source.length).trim(),
  }))
}

function subsection(body: string, title: string): string {
  const escaped = title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const heading = new RegExp(`^###\\s+${escaped}\\s*$`, 'm').exec(body)
  if (!heading) return ''
  const start = (heading.index || 0) + heading[0].length
  const remainder = body.slice(start)
  const nextHeading = /^###\s+/m.exec(remainder)
  return remainder.slice(0, nextHeading?.index ?? remainder.length).trim()
}

function firstTimestamp(value: string): { label: string; seconds: number } | undefined {
  const match = value.match(/\b(\d{1,2}:\d{2}(?::\d{2})?)\b/)
  if (!match) return undefined
  return { label: match[1], seconds: parseTimestamp(match[1]) }
}

function firstQuote(body: string): string | undefined {
  return body.split('\n')
    .filter(line => line.trim().startsWith('>'))
    .map(line => line.trim().replace(/^>\s*/, ''))
    .find(line => line && !/^\*\*.+\*\*/.test(line) && !/`?\d{1,2}:\d{2}(?::\d{2})?/.test(line))
}

export function parseOutlineWikiPage(content: string, durationSeconds = 0): Chapter[] {
  const sections = splitLevelTwoSections(content)
  let previousStart = -1
  return sections.map((section, chapterIndex) => {
    const range = section.body.match(/时间[：:]\s*`?(\d{1,2}:\d{2}(?::\d{2})?)`?\s*[–—-]\s*`?(\d{1,2}:\d{2}(?::\d{2})?)`?/)
    if (!range) throw new Error(`章节“${section.title}”缺少有效时间范围`)
    const startSeconds = parseTimestamp(range[1])
    let endSeconds = parseTimestamp(range[2])
    if (durationSeconds > 0) {
      if (startSeconds >= durationSeconds) throw new Error(`章节“${section.title}”开始时间超过视频时长`)
      endSeconds = Math.min(endSeconds, durationSeconds)
    }
    if (endSeconds <= startSeconds || startSeconds <= previousStart) throw new Error(`章节“${section.title}”时间顺序无效`)
    previousStart = startSeconds

    const summary = subsection(section.body, '本章核心内容')
      .split('\n')
      .map(line => line.trim())
      .filter(Boolean)
      .join('\n')
    const evidence = firstQuote(subsection(section.body, '原文'))
    const pointLines = subsection(section.body, '关键知识点').split('\n')
      .map(line => line.trim())
      .filter(line => /^[-*+]\s+/.test(line) && !/关键词/.test(line))
    const knowledgePoints = pointLines.map((line, pointIndex) => {
      const text = line.replace(/^[-*+]\s+/, '').replace(/\s*[（(]?`?\d{1,2}:\d{2}(?::\d{2})?`?[）)]?\s*$/, '').trim()
      const timestamp = firstTimestamp(line)
      const seconds = Math.min(Math.max(timestamp?.seconds ?? startSeconds, startSeconds), endSeconds)
      return {
        id: `chapter-${chapterIndex + 1}-point-${pointIndex + 1}`,
        title: text,
        timestamp: formatTimestamp(seconds),
        seconds,
        transcriptSnippet: evidence,
      }
    })

    return {
      id: `chapter-${chapterIndex + 1}`,
      chapter_index: String(chapterIndex + 1).padStart(2, '0'),
      chapter_title: section.title,
      start_time: formatTimestamp(startSeconds),
      start_seconds: startSeconds,
      end_time: formatTimestamp(endSeconds),
      end_seconds: endSeconds,
      chapter_summary: summary,
      knowledge_points: knowledgePoints,
    }
  })
}

export function parseSummaryWikiPage(content: string): SummarySection[] {
  return splitLevelTwoSections(content).map((section, index) => {
    const evidence = firstTimestamp(section.body)
    const transcriptSnippet = firstQuote(section.body)
    const renderedContent = section.body.split('\n')
      .filter(line => !line.trim().startsWith('>'))
      .map(line => line.trim())
      .filter(Boolean)
      .join('\n')
    return {
      id: `summary-${index + 1}`,
      title: section.title,
      content: renderedContent,
      evidenceTimestamp: evidence?.label,
      evidenceSeconds: evidence?.seconds,
      transcriptSnippet,
    }
  })
}

export function parseOverviewWikiPage(content: string): string {
  const sections = splitLevelTwoSections(content)
  if (sections.length > 0) return sections.map(section => section.body).filter(Boolean).join('\n\n').trim()
  return stripFrontmatter(content).replace(/^#\s+.+$/m, '').trim()
}

export function parseTranscriptPageWikiPage(content: string): string {
  return stripFrontmatter(content)
}

function parseSubtitleTimestamp(value: string): number {
  const normalized = value.replace(',', '.')
  const [hours, minutes, seconds] = normalized.split(':').map(Number)
  if (![hours, minutes, seconds].every(Number.isFinite) || minutes < 0 || minutes >= 60 || seconds < 0 || seconds >= 60) {
    throw new Error(`无效字幕时间戳：${value}`)
  }
  return hours * 3600 + minutes * 60 + seconds
}

export function parseSubtitleFile(content: string): SubtitleCue[] {
  const normalized = content.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n').trim()
  if (!normalized) return []
  const blocks = normalized.split(/\n{2,}/)
  const cues = blocks.flatMap(block => {
    const lines = block.split('\n').map(line => line.trim()).filter(Boolean)
    const timeIndex = lines.findIndex(line => /\d{2}:\d{2}:\d{2}[,.]\d{3}\s*-->\s*\d{2}:\d{2}:\d{2}[,.]\d{3}/.test(line))
    if (timeIndex < 0) return []
    const match = lines[timeIndex].match(/(\d{2}:\d{2}:\d{2}[,.]\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}[,.]\d{3})/)
    if (!match) return []
    const startSeconds = parseSubtitleTimestamp(match[1])
    const endSeconds = parseSubtitleTimestamp(match[2])
    if (endSeconds <= startSeconds) throw new Error('字幕时间顺序无效')
    const text = lines.slice(timeIndex + 1).join('\n').replace(/<[^>]+>/g, '').trim()
    return text ? [{ start_seconds: startSeconds, end_seconds: endSeconds, text }] : []
  })
  return cues.sort((left, right) => left.start_seconds - right.start_seconds)
}

export function mapRelatedKnowledgeResponse(videoId: string, response: BackendRelatedKnowledgeResponse) {
  const rawAnchors = response.anchors
  const grouped: BackendAnchor[] = Array.isArray(rawAnchors)
    ? rawAnchors
    : KNOWLEDGE_TYPES.flatMap(type => ((rawAnchors as Partial<Record<KnowledgeType, BackendAnchor[]>> | undefined)?.[type] || [])
        .map((item: BackendAnchor) => ({ ...item, type: item.type || type })))
  const crossVideoItems: CrossVideoKnowledgeItem[] = (response.cross_video || [])
    .filter(item => item.video_id && item.video_id !== videoId)
    .map((item, index) => ({
      id: item.id || `cross-video-${index + 1}`,
      anchorId: item.anchor_id || item.id,
      knowledge_type: item.type || 'concept',
      relation_type: RELATION_TYPES.has(item.relation_type as RelationType) ? item.relation_type as RelationType : '补充',
      knowledge_content: item.title || '关联知识',
      timestamp: item.timestamp || '00:00',
      seconds: Number.isFinite(Number(item.seconds)) ? Number(item.seconds) : item.timestamp ? parseTimestamp(item.timestamp) : 0,
      video_id: item.video_id || '',
      video_title: item.video_title || '关联视频',
      video_category: item.video_type === 'interview' ? 'interview' : item.video_type === 'tutorial' ? 'training' : item.video_type === 'case_analysis' ? 'salon' : 'general',
      relation_description: item.relation_description || '与当前内容存在知识关联。',
    }))
  const anchors: CurrentKnowledgeAnchor[] = grouped.map((item) => ({
    id: item.id,
    knowledge_type: item.type || 'concept',
    content: item.title || '未命名知识',
    timestamp: item.timestamp || '00:00',
    seconds: Number.isFinite(Number(item.seconds)) ? Number(item.seconds) : item.timestamp ? parseTimestamp(item.timestamp) : 0,
    related_count: crossVideoItems.filter(cross => cross.anchorId === item.id).length || item.related_video_ids?.length || 0,
  }))
  const relatedVideoIDs = new Set(grouped.flatMap(item => item.related_video_ids || []))
  crossVideoItems.forEach(item => relatedVideoIDs.add(item.video_id))
  const overview = response.overview ?? (anchors.length > 0 || crossVideoItems.length > 0 ? {
    relation_overview: `已从当前视频提取 ${anchors.length} 个知识锚点，其中 ${crossVideoItems.length} 条已建立跨视频关联。`,
    related_video_count: relatedVideoIDs.size,
    relation_count: crossVideoItems.length,
    top_topics: anchors.slice(0, 5).map(anchor => anchor.content),
  } satisfies RelationOverview : null)
  return { videoId, overview, anchors, crossVideoItems }
}
