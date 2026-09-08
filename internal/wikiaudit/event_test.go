package wikiaudit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventContractRequiresPhaseEvidence(t *testing.T) {
	identity := SourceIdentity{VideoID: "video-1", TranscriptGeneration: "generation-1", SourceKnowledgeID: "source-1", KnowledgeBaseID: "knowledge-kb"}
	event := New(identity, "task-1", "wiki:ingest", "ingest", "42", "map", "succeeded")
	require.ErrorContains(t, event.Validate(), "candidate_count_missing")
	event.CandidateCount = Count(3)
	require.NoError(t, event.Validate())

	page := New(identity, "task-1", "wiki:ingest", "ingest", "42", "page_write", "succeeded")
	require.ErrorContains(t, page.Validate(), "page_identity_missing")
	page.PageID, page.Slug, page.PageType, page.Version = "page-1", "concept/example", "concept", Count(2)
	require.NoError(t, page.Validate())
}

func TestRunIDIsStableAcrossSourceAndNativeConsumers(t *testing.T) {
	identity := SourceIdentity{VideoID: "video-1", TranscriptGeneration: "generation-1", SourceKnowledgeID: "source-1", KnowledgeBaseID: "knowledge-kb"}
	require.Equal(t, RunID(identity), RunID(identity))
	require.NotEqual(t, RunID(identity), RunID(SourceIdentity{VideoID: "video-1", TranscriptGeneration: "generation-2", SourceKnowledgeID: "source-2", KnowledgeBaseID: "knowledge-kb"}))
}

func TestParseStandardizedVideoSourceIdentity(t *testing.T) {
	identity, err := ParseSourceIdentity("---\ntype: video_transcript_source\nsource_video_id: video-1\ntranscript_generation: generation-1\n---\n\nbody", "source-1", "knowledge-kb")
	require.NoError(t, err)
	require.Equal(t, "video-1", identity.VideoID)
	require.Equal(t, "generation-1", identity.TranscriptGeneration)
	require.Equal(t, "source-1", identity.SourceKnowledgeID)
}

func TestKnowledgeV2PageWriteRequiresSkillTaskAndToolCall(t *testing.T) {
	identity := SourceIdentity{VideoID: "video-1", TranscriptGeneration: "generation-1", SourceKnowledgeID: "source-1", KnowledgeBaseID: "knowledge-kb"}
	event := NewKnowledgeV2PageWrite(identity, "job-1", "session-1", "extract-video-knowledge", "call-1", "created", "page-1", "concept/example", "index", 2)
	require.NoError(t, event.Validate())
	require.Equal(t, ProducerKnowledgeV2, event.PageProducer)
	require.Equal(t, "job-1", event.TaskID)
	require.Equal(t, "extract-video-knowledge", event.SkillName)
	require.Equal(t, "wiki_write_page", event.ToolName)
	require.Equal(t, "call-1", event.ToolCallID)

	event.ToolCallID = ""
	require.ErrorContains(t, event.Validate(), "knowledge_v2_provenance_incomplete")
	event.ToolCallID = "call-1"
	event.TaskID = event.SessionID
	require.ErrorContains(t, event.Validate(), "knowledge_v2_task_session_collision")
}

func TestParseKnowledgeV2PageIdentityRequiresMatchingResolvedSource(t *testing.T) {
	content := "---\nsource_video_id: video-1\nsource_document_id: source-1\ntranscript_generation: generation-1\nsource_refs: [source-1]\n---\n"
	identity, err := ParseKnowledgeV2PageIdentity(content, "kb-1", []string{"source-1|Video"})
	require.NoError(t, err)
	require.Equal(t, SourceIdentity{
		VideoID: "video-1", TranscriptGeneration: "generation-1",
		SourceKnowledgeID: "source-1", KnowledgeBaseID: "kb-1",
	}, identity)

	_, err = ParseKnowledgeV2PageIdentity(content, "kb-1", []string{"source-2"})
	require.ErrorContains(t, err, "page_source_identity_mismatch")
}
