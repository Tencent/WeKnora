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
