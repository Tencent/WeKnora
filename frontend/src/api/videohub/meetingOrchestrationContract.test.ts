import assert from 'node:assert/strict'
import test from 'node:test'
import { parseMeetingProjection } from './meetingOrchestrationContract'

const evidence = { video_id: 'part1', transcript_generation: 'g1', evidence_id: 'e1', start_ms: 0, end_ms: 1000 }

function validProjection() {
  return {
    schema_version: 'meeting-orchestration/v2', owner_scope_id: 'owner', source_fingerprint: 'fp', generated_at: '2026-01-01T00:00:00Z',
    statistics: { scanned_videos: 2, qualified_videos: 2, skipped_videos: 0, meeting_session_count: 1, possible_pair_count: 0, topic_cluster_count: 1, decision_count: 0, todo_count: 0 },
    meeting_sessions: [{ meeting_session_id: 'session-1', fragment_video_ids: ['part1', 'part2'], ordering_basis: 'content_evidence', grouping_evidence_refs: [evidence] }],
    topic_clusters: [{ cluster_id: 'topic-1', title: '网课会议知识库', business_object: '网课会议知识库', summary: 'summary', source_video_ids: ['part1', 'part2'], meeting_session_ids: ['session-1'], video_contributions: [
      { video_id: 'part1', meeting_session_id: 'session-1', contribution_type: 'session_fragment', evidence_refs: [evidence] },
      { video_id: 'part2', meeting_session_id: 'session-1', contribution_type: 'session_fragment', evidence_refs: [] },
    ], work_items: [], evolution: [], decisions: [], todos: [], knowledge: [] }],
    topic_cluster_relations: [],
  }
}

test('accepts a v2 projection with one session and two fragments', () => {
  const projection = parseMeetingProjection(validProjection())
  assert.equal(projection.meeting_sessions[0].fragment_video_ids.length, 2)
})

test('requires regeneration for a v1 projection', () => {
  assert.throws(() => parseMeetingProjection({ schema_version: 'meeting-orchestration/v1' }), /重新生成/)
})

test('rejects missing session fields and cross-video contribution evidence', () => {
  const missingSession = validProjection() as any
  delete missingSession.meeting_sessions
  assert.throws(() => parseMeetingProjection(missingSession), /数据结构不完整/)

  const wrongEvidence = validProjection() as any
  wrongEvidence.topic_clusters[0].video_contributions[0].evidence_refs = [{ ...evidence, video_id: 'part2' }]
  assert.throws(() => parseMeetingProjection(wrongEvidence), /视频贡献证据与视频不一致/)
})
