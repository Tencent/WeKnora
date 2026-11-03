package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreationRejectsInvalidFolderBeforeSideEffects(t *testing.T) {
	s := &knowledgeService{}
	opts := types.KnowledgeCreationOptions{FolderPath: "../private"}
	_, err := s.CreateKnowledgeFromManual(
		context.Background(),
		"kb",
		&types.ManualKnowledgePayload{Title: "title", Content: "body"},
		"mcp",
		opts,
	)
	require.Error(t, err)
	for _, url := range []string{"https://example.com/page", "https://example.com/report.pdf"} {
		_, err = s.CreateKnowledgeFromURL(context.Background(), "kb", url, "", "", nil, "title", nil, "mcp", nil, opts)
		require.Error(t, err)
	}
	_, err = s.createKnowledgeFromFileURLWithReplacement(
		context.Background(),
		"kb",
		"https://example.com/report.pdf",
		"",
		"pdf",
		nil,
		"title",
		nil,
		"mcp",
		nil,
		nil,
		opts,
	)
	require.Error(t, err)
}
