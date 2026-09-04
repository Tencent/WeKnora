import type {
  Chapter,
  ChapterAlignmentStatus,
  CrossVideoKnowledgeItem,
  CurrentKnowledgeAnchor,
  KnowledgeType,
  RelationOverview,
  RelationType,
  SummaryBlock,
  SummaryEvidenceRef,
  SummaryEvidence,
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
  primary_type?: KnowledgeType
  related_video_ids?: string[]
  core_content?: string
  structure_fields?: Array<{ key?: string; label?: string; value?: string }>
  evidence_ids?: string[]
  information_nature?: string
  time_range?: string
  source_video_title?: string
  related_content?: Array<{ title?: string; slug?: string; target_type?: string }>
  related_knowledge?: Array<{ title?: string; slug?: string }>
  related_entities?: Array<{ title?: string; slug?: string }>
  timestamp?: string
  seconds?: number
}

interface CanonicalKnowledgePoint {
  title?: string
  seconds?: number
  evidence_sentence_ids?: string[]
}

interface CanonicalChapter {
  chapter_index?: number
  chapter_title?: string
  start_seconds?: number
  end_seconds?: number
  chapter_summary?: string
  knowledge_points?: CanonicalKnowledgePoint[]
  alignment_status?: Chapter['alignment_status']
  evidence_sentence_ids?: string[]
}

export interface CanonicalOutlineResponse {
  schema_version?: number
  chapters?: CanonicalChapter[]
  content?: string
}

interface BackendCrossVideoItem extends BackendAnchor {
  anchor_id?: string
  relation_type?: RelationType
  relation_description?: string
  video_id?: string
  video_title?: string
  video_type?: string
  source_chapter?: string
  source_chapter_title?: string
  chapter_title?: string
}

export interface BackendRelatedKnowledgeResponse {
  anchors?: Partial<Record<KnowledgeType, BackendAnchor[]>> | BackendAnchor[]
  cross_video?: BackendCrossVideoItem[]
  overview?: RelationOverview | null
}

const KNOWLEDGE_TYPES: KnowledgeType[] = ['entity', 'concept', 'case', 'methodology', 'insight']
const RELATION_TYPES = new Set<RelationType>(['contradicts', 'complements', 'explains', 'example_of', 'part_of', 'derived_from', 'supports', 'related_to'])

function isKnowledgeType(value: unknown): value is KnowledgeType {
  return typeof value === 'string' && KNOWLEDGE_TYPES.includes(value as KnowledgeType)
}

function hasKnowledgeType<T extends BackendAnchor>(item: T): item is T & { type: KnowledgeType } {
  const type = isKnowledgeType(item.primary_type) ? item.primary_type : item.type
  if (!isKnowledgeType(type)) return false
  item.type = type
  return true
}

function normalizeWikiLinks(items: Array<{ title?: string; slug?: string; target_type?: string }> | undefined) {
  return (items || [])
    .map(item => ({ title: item.title?.trim() || item.slug?.trim() || '', slug: item.slug?.trim() || undefined, targetType: item.target_type?.trim() || undefined }))
    .filter(item => item.title)
}

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

function parseCanonicalOutline(response: CanonicalOutlineResponse, durationSeconds: number): Chapter[] {
  if (response.schema_version !== 1 || !Array.isArray(response.chapters) || response.chapters.length === 0) {
    throw new Error('章节 JSON Schema v1 内容无效')
  }
  let previousStart = -1
  let previousEnd = 0
  return response.chapters.map((chapter, chapterIndex) => {
    const startSeconds = chapter.start_seconds
    const endSeconds = chapter.end_seconds
    if (typeof startSeconds !== 'number' || typeof endSeconds !== 'number' || !Number.isInteger(startSeconds) || !Number.isInteger(endSeconds) || startSeconds < 0 || endSeconds <= startSeconds) {
      throw new Error(`章节 ${chapterIndex + 1} 时间范围无效`)
    }
    if (durationSeconds > 0 && (startSeconds < 0 || endSeconds > durationSeconds)) {
      throw new Error(`章节 ${chapterIndex + 1} 超出视频时长`)
    }
    if (startSeconds <= previousStart || (chapterIndex > 0 && startSeconds < previousEnd)) {
      throw new Error(`章节 ${chapterIndex + 1} 时间顺序无效`)
    }
    if (!chapter.chapter_title?.trim() || !chapter.chapter_summary?.trim() || !Array.isArray(chapter.knowledge_points) || chapter.knowledge_points.length === 0 || chapter.knowledge_points.length > 3) {
      throw new Error(`章节 ${chapterIndex + 1} 内容不完整`)
    }
    previousStart = startSeconds
    previousEnd = endSeconds
    return {
      id: `chapter-${chapterIndex + 1}`,
      chapter_index: String(chapter.chapter_index ?? chapterIndex + 1).padStart(2, '0'),
      chapter_title: chapter.chapter_title.trim(),
      start_time: formatTimestamp(startSeconds),
      start_seconds: startSeconds,
      end_time: formatTimestamp(endSeconds),
      end_seconds: endSeconds,
      chapter_summary: chapter.chapter_summary.trim(),
      knowledge_points: chapter.knowledge_points.map((point, pointIndex) => {
        if (!point.title?.trim() || typeof point.seconds !== 'number' || !Number.isInteger(point.seconds)) {
          throw new Error(`章节 ${chapterIndex + 1} 知识点 ${pointIndex + 1} 内容无效`)
        }
        if (point.seconds < startSeconds || point.seconds > endSeconds) {
          throw new Error(`章节 ${chapterIndex + 1} 知识点 ${pointIndex + 1} 时间范围无效`)
        }
        return {
          id: `chapter-${chapterIndex + 1}-point-${pointIndex + 1}`,
          title: point.title.trim(),
          timestamp: formatTimestamp(point.seconds),
          seconds: point.seconds,
          evidenceSentenceIds: Array.isArray(point.evidence_sentence_ids) ? point.evidence_sentence_ids.filter((id): id is string => typeof id === 'string' && id.trim().length > 0) : undefined,
        }
      }),
      alignment_status: chapter.alignment_status,
      evidenceSentenceIds: Array.isArray(chapter.evidence_sentence_ids) ? chapter.evidence_sentence_ids.filter((id): id is string => typeof id === 'string' && id.trim().length > 0) : undefined,
    }
  })
}

export function parseOutlineResponse(response: CanonicalOutlineResponse, durationSeconds = 0): Chapter[] {
  if (response.schema_version !== undefined || response.chapters !== undefined) {
    return parseCanonicalOutline(response, durationSeconds)
  }
  if (!response.content?.trim()) throw new Error('章节内容为空')
  return parseOutlineWikiPage(response.content, durationSeconds)
}

export function formatTimestamp(seconds: number): string {
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

function parseAlignmentStatus(body: string): ChapterAlignmentStatus {
  const match = body.match(/对齐状态[：:]\s*`?(verified|aligned|pending_alignment)`?/i)
  return (match?.[1]?.toLowerCase() as ChapterAlignmentStatus | undefined) || 'pending_alignment'
}

function parseSourceContent(body: string) {
  const source = subsection(body, '原文')
  if (!source) return []
  return source.split(/\n\s*\n/).flatMap((block) => {
    const lines = block.split('\n').map(line => line.trim()).filter(Boolean)
    const header = lines[0]?.match(/^>\s*\*\*(.+?)\*\*\s+`?(\d{1,2}:\d{2}(?::\d{2})?)`?\s*[–—-]\s*`?(\d{1,2}:\d{2}(?::\d{2})?)`?\s*$/)
    if (!header) return []
    const content = lines.slice(1).map(line => line.replace(/^>\s?/, '').trim()).filter(Boolean).join('\n')
    if (!content) return []
    const startSeconds = parseTimestamp(header[2])
    const endSeconds = parseTimestamp(header[3])
    if (endSeconds <= startSeconds) return []
    return [{
      speaker: header[1].trim(),
      start_time: formatTimestamp(startSeconds),
      start_seconds: startSeconds,
      end_time: formatTimestamp(endSeconds),
      end_seconds: endSeconds,
      content,
    }]
  })
}

export function parseOutlineWikiPage(content: string, durationSeconds = 0): Chapter[] {
  const sections = splitLevelTwoSections(content)
  if (sections.length === 0) throw new Error('章节内容缺少章节标题')
  let previousStart = -1
  let previousEnd = 0
  return sections.map((section, chapterIndex) => {
    const range = section.body.match(/时间[：:]\s*`?(\d{1,2}:\d{2}(?::\d{2})?)`?\s*[–—-]\s*`?(\d{1,2}:\d{2}(?::\d{2})?)`?/) || section.title.match(/[（(]\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*[–—-]\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*[）)]/)
    if (!range) throw new Error(`章节“${section.title}”缺少有效时间范围`)
    const startSeconds = parseTimestamp(range[1])
    let endSeconds = parseTimestamp(range[2])
    if (durationSeconds > 0) {
      if (startSeconds >= durationSeconds) throw new Error(`章节“${section.title}”开始时间超过视频时长`)
      endSeconds = Math.min(endSeconds, durationSeconds)
    }
    if (endSeconds <= startSeconds || startSeconds <= previousStart || (chapterIndex > 0 && startSeconds < previousEnd)) throw new Error(`章节“${section.title}”时间顺序无效`)
    previousStart = startSeconds
    previousEnd = endSeconds

    const summary = subsection(section.body, '本章核心内容')
      .split('\n')
      .map(line => line.trim())
      .filter(Boolean)
      .join('\n')
    const sourceContent = parseSourceContent(section.body)
    const evidence = sourceContent[0]?.content || firstQuote(subsection(section.body, '原文'))
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
      chapter_title: section.title.replace(/[（(]\s*\d{1,2}:\d{2}(?::\d{2})?\s*[–—-]\s*\d{1,2}:\d{2}(?::\d{2})?\s*[）)]\s*$/, '').trim(),
      start_time: formatTimestamp(startSeconds),
      start_seconds: startSeconds,
      end_time: formatTimestamp(endSeconds),
      end_seconds: endSeconds,
      chapter_summary: summary,
      knowledge_points: knowledgePoints,
      alignment_status: parseAlignmentStatus(section.body),
      source_content: sourceContent,
    }
  })
}

const SUMMARY_TITLES: Record<string, readonly string[]> = {
  interview: ['一、人物背景', '二、经历与决策', '三、核心观点', '四、原则与思维模型', '五、案例与证据', '六、反思与边界'],
  training: ['一、目标与受众', '二、知识地图', '三、核心概念', '四、方法与步骤', '五、示例与异常', '六、练习与应用'],
  salon: ['一、活动与参与者', '二、议题与观点', '三、观点交锋', '四、案例与问答', '五、共识与分歧', '六、探索方向'],
  general: ['一、定位与问题', '二、主张与论证', '三、证据与案例', '四、限定与反方', '五、影响与建议'],
}

export interface StructuredSummaryResponse {
  schemaVersion?: number
  videoType?: string
  sections?: unknown
}

export function parseStructuredSummary(response: StructuredSummaryResponse, category: string): SummarySection[] {
  const titles = SUMMARY_TITLES[category]
  if (response.schemaVersion !== 1 || response.videoType !== category || !Array.isArray(response.sections) || response.sections.length !== titles.length) {
    throw new Error('智能总结结构不符合当前模板')
  }
  return response.sections.map((rawSection, sectionIndex) => {
    if (!rawSection || typeof rawSection !== 'object') throw new Error(`智能总结第 ${sectionIndex + 1} 章无效`)
    const section = rawSection as Record<string, unknown>
    if (section.title !== titles[sectionIndex] || typeof section.id !== 'string' || !section.id.trim() || !Array.isArray(section.blocks) || section.blocks.length === 0) {
      throw new Error(`智能总结章节标题或内容不符合模板：${titles[sectionIndex]}`)
    }
    return {
      id: section.id,
      title: section.title,
      blocks: section.blocks.map((rawBlock, blockIndex) => parseSummaryBlock(rawBlock, sectionIndex, blockIndex)),
    }
  })
}

function parseSummaryBlock(rawBlock: unknown, sectionIndex: number, blockIndex: number): SummaryBlock {
  if (!rawBlock || typeof rawBlock !== 'object') throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条无效`)
  const block = rawBlock as Record<string, unknown>
  const kind = block.kind
  const text = typeof block.text === 'string' ? block.text.trim() : ''
  if (typeof block.id !== 'string' || !block.id.trim() || (kind !== 'paragraph' && kind !== 'bullet') || !text || containsMarkdown(text) || !Array.isArray(block.evidenceChunkIds) || !Array.isArray(block.evidence) || block.evidence.length === 0 || block.evidenceChunkIds.length !== block.evidence.length) {
    throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条不符合渲染契约`)
  }
  const evidence = block.evidence.map((rawEvidence, evidenceIndex) => parseSummaryEvidence(rawEvidence, sectionIndex, blockIndex, evidenceIndex))
  const evidenceChunkIds = block.evidenceChunkIds
  if (evidenceChunkIds.some((chunkId, index) => typeof chunkId !== 'string' || !chunkId.trim() || chunkId !== evidence[index].chunkId)) {
    throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条出处与分块不一致`)
  }
  const knowledgeRefs = parseStringReferences(block.knowledge_refs ?? block.knowledgeRefs, sectionIndex, blockIndex, 'knowledge_refs')
  const evidenceRefs = parseEvidenceReferences(block.evidence_refs ?? block.evidenceRefs, evidence, sectionIndex, blockIndex)
  return {
    id: block.id,
    kind,
    text,
    evidence,
    ...(knowledgeRefs ? { knowledgeRefs } : {}),
    ...(evidenceRefs ? { evidenceRefs } : {}),
  }
}

function parseStringReferences(raw: unknown, sectionIndex: number, blockIndex: number, field: string): string[] | undefined {
  if (raw === undefined) return undefined
  if (!Array.isArray(raw) || raw.some(item => typeof item !== 'string' || !item.trim())) {
    throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条 ${field} 无效`)
  }
  return raw.map(item => item.trim())
}

function parseEvidenceReferences(raw: unknown, evidence: SummaryEvidence[], sectionIndex: number, blockIndex: number): SummaryEvidenceRef[] | undefined {
  if (raw === undefined) return undefined
  if (!Array.isArray(raw) || raw.length !== evidence.length) {
    throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条 evidence_refs 与出处数量不一致`)
  }
  return raw.map((item, evidenceIndex) => {
    if (!item || typeof item !== 'object') throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条 evidence_ref ${evidenceIndex + 1} 无效`)
    const ref = item as Record<string, unknown>
    const evidenceSentenceId = typeof (ref.evidence_sentence_id ?? ref.evidenceSentenceId) === 'string' ? String(ref.evidence_sentence_id ?? ref.evidenceSentenceId).trim() : ''
    const startMs = ref.start_ms ?? ref.startMs
    const endMs = ref.end_ms ?? ref.endMs
    const chunkId = ref.chunk_id ?? ref.chunkId
    if (!evidenceSentenceId || typeof startMs !== 'number' || typeof endMs !== 'number' || !Number.isInteger(startMs) || !Number.isInteger(endMs) || startMs < 0 || endMs <= startMs || (chunkId !== undefined && (typeof chunkId !== 'string' || !chunkId.trim()))) {
      throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条 evidence_ref ${evidenceIndex + 1} 无效`)
    }
    const source = evidence[evidenceIndex]
    if (source.evidenceSentenceId !== evidenceSentenceId || Math.round(source.startSeconds * 1000) !== startMs || Math.round(source.endSeconds * 1000) !== endMs || (chunkId !== undefined && chunkId !== source.chunkId)) {
      throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条 evidence_ref ${evidenceIndex + 1} 与出处不一致`)
    }
    return { evidenceSentenceId, startMs, endMs, ...(typeof chunkId === 'string' ? { chunkId: chunkId.trim() } : {}) }
  })
}

function parseSummaryEvidence(rawEvidence: unknown, sectionIndex: number, blockIndex: number, evidenceIndex: number): SummaryEvidence {
  if (!rawEvidence || typeof rawEvidence !== 'object') throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条出处无效`)
  const evidence = rawEvidence as Record<string, unknown>
  if (typeof evidence.chunkId !== 'string' || !evidence.chunkId.trim() || typeof evidence.evidenceSentenceId !== 'string' || !evidence.evidenceSentenceId.trim() || typeof evidence.startSeconds !== 'number' || typeof evidence.endSeconds !== 'number' || !Number.isFinite(evidence.startSeconds) || !Number.isFinite(evidence.endSeconds) || evidence.startSeconds < 0 || evidence.endSeconds <= evidence.startSeconds || typeof evidence.timestamp !== 'string' || !evidence.timestamp.trim() || typeof evidence.transcriptSnippet !== 'string' || !evidence.transcriptSnippet.trim()) {
    throw new Error(`智能总结第 ${sectionIndex + 1} 章第 ${blockIndex + 1} 条出处 ${evidenceIndex + 1} 无效`)
  }
  return {
    chunkId: evidence.chunkId,
    evidenceSentenceId: evidence.evidenceSentenceId.trim(),
    startSeconds: evidence.startSeconds,
    endSeconds: evidence.endSeconds,
    timestamp: evidence.timestamp,
    transcriptSnippet: evidence.transcriptSnippet.trim(),
  }
}

function containsMarkdown(value: string): boolean {
  return value.includes('```') || /(^|\n)\s{0,3}(#{1,6}|[-*+]\s|\d+[.)]\s)/.test(value)
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
  const parts = normalized.split(':').map(Number)
  if ((parts.length !== 2 && parts.length !== 3) || !parts.every(Number.isFinite)) {
    throw new Error(`无效字幕时间戳：${value}`)
  }
  const seconds = parts[parts.length - 1]
  const minutes = parts[parts.length - 2]
  if (minutes < 0 || minutes >= 60 || seconds < 0 || seconds >= 60) throw new Error(`无效字幕时间戳：${value}`)
  return parts.length === 2 ? minutes * 60 + seconds : parts[0] * 3600 + minutes * 60 + seconds
}

export function parseSubtitleFile(content: string): SubtitleCue[] {
  const normalized = content.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n').trim()
  if (!normalized) return []
  const blocks = normalized.split(/\n{2,}/)
  const cues = blocks.flatMap(block => {
    const lines = block.split('\n').map(line => line.trim()).filter(Boolean)
    const timeIndex = lines.findIndex(line => /(?:\d{2}:)?\d{2}:\d{2}[,.]\d{3,}\s*-->\s*(?:\d{2}:)?\d{2}:\d{2}[,.]\d{3,}/.test(line) || /\d{2}:\d{2}[,.]\d{3,}\s*-->\s*\d{2}:\d{2}[,.]\d{3,}/.test(line))
    if (timeIndex < 0) return []
    const match = lines[timeIndex].match(/((?:\d{2}:)?\d{2}:\d{2}[,.]\d{3,}|\d{2}:\d{2}[,.]\d{3,})\s*-->\s*((?:\d{2}:)?\d{2}:\d{2}[,.]\d{3,}|\d{2}:\d{2}[,.]\d{3,})/)
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
    .filter(item => item.video_id && item.video_id !== videoId && hasKnowledgeType(item))
    .map((item, index) => ({
      id: item.id || `cross-video-${index + 1}`,
      anchorId: item.anchor_id || item.id,
      knowledge_type: item.type as KnowledgeType,
      relation_type: RELATION_TYPES.has(item.relation_type as RelationType) ? item.relation_type as RelationType : 'related_to',
      knowledge_content: item.title || '关联知识',
      timestamp: item.timestamp || '00:00',
      seconds: Number.isFinite(Number(item.seconds)) ? Number(item.seconds) : item.timestamp ? parseTimestamp(item.timestamp) : 0,
      video_id: item.video_id || '',
      video_title: item.video_title || '关联视频',
      video_category: item.video_type === 'interview' ? 'interview' : item.video_type === 'tutorial' || item.video_type === 'training' ? 'training' : item.video_type === 'lecture' || item.video_type === 'salon' ? 'salon' : 'general',
      relation_description: item.relation_description || '与当前内容存在知识关联。',
      source_chapter: item.source_chapter?.trim() || item.source_chapter_title?.trim() || item.chapter_title?.trim() || '',
    }))
  const anchors: CurrentKnowledgeAnchor[] = grouped.filter(hasKnowledgeType).map((item) => ({
    id: item.id,
    knowledge_type: item.type,
    content: item.title || '未命名知识',
    coreContent: item.core_content?.trim() || '',
    structureFields: (item.structure_fields || [])
      .filter(field => field.key?.trim() && field.label?.trim() && field.value?.trim())
      .map(field => ({ key: field.key!.trim(), label: field.label!.trim(), value: field.value!.trim() })),
    informationNature: item.information_nature?.trim() || '',
    timeRange: item.time_range?.trim() || '',
    sourceVideoTitle: item.source_video_title?.trim() || '',
    relatedContent: normalizeWikiLinks(item.related_content?.length ? item.related_content : [...(item.related_knowledge || []), ...(item.related_entities || [])]),
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
