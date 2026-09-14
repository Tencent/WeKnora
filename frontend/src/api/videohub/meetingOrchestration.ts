import { get, post } from '@/utils/request'

export type MeetingJob = { id: string; status: 'queued'|'running'|'succeeded'|'failed'; stage?: string; progress: number; error_code?: string; error_message?: string }
export type MeetingEvidenceRef = { video_id: string; transcript_generation: string; evidence_id: string; start_ms: number; end_ms: number }
export type MeetingProjection = {
  schema_version: 'meeting-orchestration/v1'; owner_scope_id: string; source_fingerprint: string; generated_at: string
  statistics: { scanned_videos: number; qualified_videos: number; skipped_videos: number; topic_cluster_count: number; decision_count: number; todo_count: number }
  topic_clusters: Array<{ cluster_id: string; title: string; business_object: string; summary: string; source_video_ids: string[]; work_items: Array<{ work_item_id: string; title: string; status: string; current_conclusion?: string; evidence_refs: MeetingEvidenceRef[] }>; evolution: Array<{ id: string; video_id: string; meeting_title: string; occurred_at?: string; summary: string; change: string; evidence_refs: MeetingEvidenceRef[] }>; decisions: Array<{ id: string; text: string; video_id: string; status?: string; evidence_refs: MeetingEvidenceRef[] }>; todos: Array<{ id: string; title: string; owner?: string; due?: string; status: string; video_id: string; evidence_refs: MeetingEvidenceRef[] }>; knowledge: Array<{ knowledge_object_id: string; wiki_page_id: string; knowledge_type: string; title: string }> }>
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
function normalizeLegacyArrays(value: unknown, keys: string[]) {
  if (!value || typeof value !== 'object') return
  const record = value as Record<string, unknown>
  for (const key of keys) if (record[key] === null || record[key] === undefined) record[key] = []
}
function parseProjection(value: unknown): MeetingProjection {
  if (!value || typeof value !== 'object' || (value as MeetingProjection).schema_version !== 'meeting-orchestration/v1') throw new Error('会议主题簇版本不受支持')
  const projection = value as MeetingProjection
  if (!Array.isArray(projection.topic_clusters) || !Array.isArray(projection.topic_cluster_relations) || !projection.statistics) throw new Error('会议主题簇数据结构不完整')
  const statisticKeys = ['scanned_videos', 'qualified_videos', 'skipped_videos', 'topic_cluster_count', 'decision_count', 'todo_count'] as const
  if (statisticKeys.some(key => !Number.isInteger(projection.statistics[key]) || projection.statistics[key] < 0)) throw new Error('会议主题簇统计数据不完整')
  for (const cluster of projection.topic_clusters) {
    normalizeLegacyArrays(cluster, ['source_video_ids', 'work_items', 'evolution', 'decisions', 'todos', 'knowledge'])
    if (!cluster.cluster_id || !cluster.title || !Array.isArray(cluster.source_video_ids) || !Array.isArray(cluster.work_items) || !Array.isArray(cluster.evolution) || !Array.isArray(cluster.decisions) || !Array.isArray(cluster.todos) || !Array.isArray(cluster.knowledge)) throw new Error('会议主题簇数据结构不完整')
    if (cluster.source_video_ids.some(id => typeof id !== 'string' || !id.trim())) throw new Error('会议主题簇来源视频不完整')
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
    if (sourceRefs !== undefined || targetRefs !== undefined) {
      assertEvidenceRefs(sourceRefs)
      assertEvidenceRefs(targetRefs)
    } else {
      assertEvidenceRefs(relation.evidence_refs)
    }
  }
  return projection
}
function parseJob(value: unknown): MeetingJob {
  const job = value as MeetingJob
  if (!job || typeof job.id !== 'string' || !['queued', 'running', 'succeeded', 'failed'].includes(job.status) || !Number.isFinite(job.progress)) throw new Error('会议主题簇任务状态不可用')
  return job
}
export async function fetchCurrentMeetingProjection(): Promise<MeetingProjection | null> { const response: any = await get('/api/custom/meeting-orchestration/current'); const value = response?.data; return value ? parseProjection(value) : null }
export async function generateMeetingProjection(): Promise<MeetingJob> { const response: any = await post('/api/custom/meeting-orchestration/generate'); return parseJob(response?.data) }
export async function fetchMeetingJob(id: string): Promise<MeetingJob> { const response: any = await get(`/api/custom/meeting-orchestration/jobs/${encodeURIComponent(id)}`); return parseJob(response?.data) }
