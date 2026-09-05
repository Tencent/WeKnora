package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/wikiaudit"
	"github.com/stretchr/testify/require"
)

func TestAdaptVideoKnowledgeInstructionsUsesOnlySourceIdentity(t *testing.T) {
	trace, err := adaptVideoKnowledgeInstructions("native extraction", wikiAuditContext{
		Identity: wikiaudit.SourceIdentity{
			SourceKnowledgeID:    "source-1",
			VideoID:              "video-1",
			TranscriptGeneration: "generation-1",
		},
	})
	require.NoError(t, err)
	require.Contains(t, trace.Prompt, "source_document_id: source-1")
	require.Contains(t, trace.Prompt, "source_video_id: video-1")
	require.Contains(t, trace.Prompt, "transcript_generation: generation-1")
	require.Contains(t, trace.Prompt, "保留 WeKnora 原生 Map-Reduce")
	require.NotContains(t, trace.Prompt, "字幕正文")
	require.NotContains(t, trace.Prompt, "chunk-1")
}

func TestAdaptVideoKnowledgeInstructionsDefaultsToNativeProtocol(t *testing.T) {
	trace, err := adaptVideoKnowledgeInstructions("", wikiAuditContext{
		Identity: wikiaudit.SourceIdentity{
			SourceKnowledgeID:    "source-1",
			VideoID:              "video-1",
			TranscriptGeneration: "generation-1",
		},
	})
	require.NoError(t, err)
	require.Contains(t, trace.Prompt, "Use the native WeKnora extraction protocol.")
}

func TestAdaptVideoKnowledgeInstructionsFailsClosedForIncompleteAuditIdentity(t *testing.T) {
	_, err := adaptVideoKnowledgeInstructions("native extraction", wikiAuditContext{
		Identity: wikiaudit.SourceIdentity{SourceKnowledgeID: "source-1", VideoID: "video-1"},
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "identity"))
}
