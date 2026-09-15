export type MeetingEvidenceRef = { video_id: string; transcript_generation: string; evidence_id: string; start_ms: number; end_ms: number }
export type MeetingSession = { meeting_session_id: string; fragment_video_ids: string[]; ordering_basis: string; grouping_evidence_refs: MeetingEvidenceRef[] }
export type MeetingVideoContribution = { video_id: string; meeting_session_id: string; contribution_type: string; evidence_refs: MeetingEvidenceRef[] }
export type MeetingProjection = {
  schema_version: 'meeting-orchestration/v2'; owner_scope_id: string; source_fingerprint: string; generated_at: string
  statistics: { scanned_videos: number; qualified_videos: number; skipped_videos: number; meeting_session_count: number; possible_pair_count: number; topic_cluster_count: number; decision_count: number; todo_count: number }
  meeting_sessions: MeetingSession[]
  topic_clusters: Array<{ cluster_id: string; title: string; business_object: string; summary: string; source_video_ids: string[]; meeting_session_ids: string[]; video_contributions: MeetingVideoContribution[]; work_items: Array<{ work_item_id: string; title: string; status: string; current_conclusion?: string; evidence_refs: MeetingEvidenceRef[] }>; evolution: Array<{ id: string; video_id: string; meeting_session_id?: string; work_item_id?: string; meeting_title: string; occurred_at?: string; summary: string; change: string; change_type?: string; previous_state?: string; next_state?: string; evidence_refs: MeetingEvidenceRef[] }>; decisions: Array<{ id: string; text: string; video_id: string; meeting_session_id?: string; status?: string; evidence_refs: MeetingEvidenceRef[] }>; todos: Array<{ id: string; title: string; owner?: string; due?: string; status: string; video_id: string; meeting_session_id?: string; evidence_refs: MeetingEvidenceRef[] }>; knowledge: Array<{ knowledge_object_id: string; wiki_page_id: string; knowledge_type: string; title: string }> }>
  topic_cluster_relations: Array<{ relation_id: string; source_cluster_id: string; target_cluster_id: string; relation_type: string; summary: string; source_evidence_refs?: MeetingEvidenceRef[]; target_evidence_refs?: MeetingEvidenceRef[]; evidence_refs?: MeetingEvidenceRef[] }>
}

function assertEvidenceRef(value: unknown): asserts value is MeetingEvidenceRef {
  const ref = value as MeetingEvidenceRef
  if (!ref || typeof ref.video_id !== 'string' || !ref.video_id.trim() || typeof ref.transcript_generation !== 'string' || !ref.transcript_generation.trim() || typeof ref.evidence_id !== 'string' || !ref.evidence_id.trim() || !Number.isInteger(ref.start_ms) || !Number.isInteger(ref.end_ms) || ref.start_ms < 0 || ref.end_ms <= ref.start_ms) throw new Error('会议主题簇包含不可定位的证据')
}
function assertEvidenceRefs(value: unknown): asserts value is MeetingEvidenceRef[] {
  if (!Array.isArray(value)) throw new Error('会议主题簇证据结构不完整')
  value.forEach(assertEvidenceRef)
}
function assertMeetingSession(value: unknown): asserts value is MeetingSession {
  const session = value as MeetingSession
  if (!session || typeof session.meeting_session_id !== 'string' || !session.meeting_session_id.trim() || !Array.isArray(session.fragment_video_ids) || session.fragment_video_ids.length === 0 || session.fragment_video_ids.some(id => typeof id !== 'string' || !id.trim()) || typeof session.ordering_basis !== 'string') throw new Error('会议场次数据结构不完整')
  assertEvidenceRefs(session.grouping_evidence_refs)
  for (const ref of session.grouping_evidence_refs) if (!session.fragment_video_ids.includes(ref.video_id)) throw new Error('会议场次证据不属于场次片段')
}
function assertVideoContributions(value: unknown): asserts value is MeetingVideoContribution[] {
  if (!Array.isArray(value)) throw new Error('会议视频贡献结构不完整')
  value.forEach(contribution => {
    const item = contribution as MeetingVideoContribution
    if (!item || typeof item.video_id !== 'string' || !item.video_id.trim() || typeof item.meeting_session_id !== 'string' || !item.meeting_session_id.trim() || typeof item.contribution_type !== 'string' || !item.contribution_type.trim()) throw new Error('会议视频贡献结构不完整')
    assertEvidenceRefs(item.evidence_refs)
  })
}
function normalizeLegacyArrays(value: unknown, keys: string[]) {
  if (!value || typeof value !== 'object') return
  const record = value as Record<string, unknown>
  for (const key of keys) if (record[key] === null || record[key] === undefined) record[key] = []
}
function assertProjectionRelationships(projection: MeetingProjection) {
  const sessionVideos = new Map<string, string>()
  const sessions = new Set<string>()
  for (const session of projection.meeting_sessions) {
    if (sessions.has(session.meeting_session_id)) throw new Error('会议场次标识重复')
    sessions.add(session.meeting_session_id)
    for (const videoId of session.fragment_video_ids) {
      const previous = sessionVideos.get(videoId)
      if (previous && previous !== session.meeting_session_id) throw new Error('视频片段不能属于多个会议场次')
      sessionVideos.set(videoId, session.meeting_session_id)
    }
  }
  if (projection.statistics.meeting_session_count !== projection.meeting_sessions.length) throw new Error('会议场次统计与明细不一致')
  for (const cluster of projection.topic_clusters) {
    const clusterSessions = new Set(cluster.meeting_session_ids)
    for (const sessionId of clusterSessions) if (!sessions.has(sessionId)) throw new Error('会议主题簇引用了不存在的场次')
    const contributions = new Set<string>()
    for (const contribution of cluster.video_contributions) {
      if (contributions.has(contribution.video_id)) throw new Error('会议主题簇重复记录视频贡献')
      contributions.add(contribution.video_id)
      const sessionId = sessionVideos.get(contribution.video_id)
      if (!sessionId || sessionId !== contribution.meeting_session_id || !clusterSessions.has(contribution.meeting_session_id)) throw new Error('视频贡献与会议场次不一致')
      for (const ref of contribution.evidence_refs) if (ref.video_id !== contribution.video_id) throw new Error('视频贡献证据与视频不一致')
    }
    const sourceVideos = new Set(cluster.source_video_ids)
    if (sourceVideos.size !== contributions.size || [...sourceVideos].some(videoId => !contributions.has(videoId))) throw new Error('主题簇来源视频与贡献记录不一致')
    for (const event of cluster.evolution) if (event.meeting_session_id && sessionVideos.get(event.video_id) !== event.meeting_session_id) throw new Error('演变事件与会议场次不一致')
    for (const decision of cluster.decisions) if (decision.meeting_session_id && sessionVideos.get(decision.video_id) !== decision.meeting_session_id) throw new Error('决策与会议场次不一致')
    for (const todo of cluster.todos) if (todo.meeting_session_id && sessionVideos.get(todo.video_id) !== todo.meeting_session_id) throw new Error('待办与会议场次不一致')
  }
}
export function parseMeetingProjection(value: unknown): MeetingProjection {
  if (!value || typeof value !== 'object' || (value as MeetingProjection).schema_version !== 'meeting-orchestration/v2') throw new Error('会议主题簇需要重新生成')
  const projection = value as MeetingProjection
  if (!Array.isArray(projection.meeting_sessions) || !Array.isArray(projection.topic_clusters) || !Array.isArray(projection.topic_cluster_relations) || !projection.statistics) throw new Error('会议主题簇数据结构不完整')
  projection.meeting_sessions.forEach(assertMeetingSession)
  const statisticKeys = ['scanned_videos', 'qualified_videos', 'skipped_videos', 'meeting_session_count', 'possible_pair_count', 'topic_cluster_count', 'decision_count', 'todo_count'] as const
  if (statisticKeys.some(key => !Number.isInteger(projection.statistics[key]) || projection.statistics[key] < 0)) throw new Error('会议主题簇统计数据不完整')
  for (const cluster of projection.topic_clusters) {
    normalizeLegacyArrays(cluster, ['source_video_ids', 'meeting_session_ids', 'video_contributions', 'work_items', 'evolution', 'decisions', 'todos', 'knowledge'])
    if (!cluster.cluster_id || !cluster.title || !Array.isArray(cluster.source_video_ids) || !Array.isArray(cluster.meeting_session_ids) || !Array.isArray(cluster.video_contributions) || !Array.isArray(cluster.work_items) || !Array.isArray(cluster.evolution) || !Array.isArray(cluster.decisions) || !Array.isArray(cluster.todos) || !Array.isArray(cluster.knowledge)) throw new Error('会议主题簇数据结构不完整')
    if (cluster.source_video_ids.some(id => typeof id !== 'string' || !id.trim())) throw new Error('会议主题簇来源视频不完整')
    if (cluster.meeting_session_ids.some(id => typeof id !== 'string' || !id.trim())) throw new Error('会议主题簇来源场次不完整')
    assertVideoContributions(cluster.video_contributions)
    for (const item of cluster.work_items) { normalizeLegacyArrays(item, ['evidence_refs']); assertEvidenceRefs(item.evidence_refs) }
    for (const event of cluster.evolution) { normalizeLegacyArrays(event, ['evidence_refs']); assertEvidenceRefs(event.evidence_refs) }
    for (const decision of cluster.decisions) { normalizeLegacyArrays(decision, ['evidence_refs']); assertEvidenceRefs(decision.evidence_refs) }
    for (const todo of cluster.todos) { normalizeLegacyArrays(todo, ['evidence_refs']); assertEvidenceRefs(todo.evidence_refs) }
  }
  for (const relation of projection.topic_cluster_relations) {
    normalizeLegacyArrays(relation, ['evidence_refs'])
    if (!relation.relation_id || !relation.source_cluster_id || !relation.target_cluster_id || !relation.summary) throw new Error('会议主题簇关系数据不完整')
    const sourceRefs = relation.source_evidence_refs
    const targetRefs = relation.target_evidence_refs
    if (sourceRefs !== undefined || targetRefs !== undefined) { assertEvidenceRefs(sourceRefs); assertEvidenceRefs(targetRefs) } else assertEvidenceRefs(relation.evidence_refs)
  }
  assertProjectionRelationships(projection)
  return projection
}
