package service

import (
	"context"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The bounds mirror the initialization wizard's documentSplitting binding
// (min=100,max=10000). A chunk_size of 1 was measured to balloon a 100KB
// document from 147 to 4234 chunks — 28.8x DB writes and embedding calls on
// the shared ingestion queue from a single upload (#3539).
func TestValidateChunkingSizeBounds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     types.ChunkingConfig
		wantErr bool
	}{
		{"zero chunk size means unset and passes", types.ChunkingConfig{}, false},
		{"wizard defaults pass", types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 80}, false},
		{"lower bound is inclusive", types.ChunkingConfig{ChunkSize: 100}, false},
		{"upper bound is inclusive", types.ChunkingConfig{ChunkSize: 10000}, false},
		{"one below lower bound", types.ChunkingConfig{ChunkSize: 99}, true},
		{"the reported amplification value", types.ChunkingConfig{ChunkSize: 1}, true},
		{"one above upper bound", types.ChunkingConfig{ChunkSize: 10001}, true},
		{"negative overlap", types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: -1}, true},
		{"zero overlap passes", types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateChunkingSizeBounds(tc.cfg)
			if tc.wantErr {
				var badReq *werrors.AppError
				require.ErrorAs(t, err, &badReq)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateProcessOverrides_RejectsChunkSizeOutOfBounds(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{}
	err := ValidateProcessOverrides(context.Background(), kb, &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 1},
	}, []string{"txt"})
	var badReq *werrors.AppError
	require.ErrorAs(t, err, &badReq)

	require.NoError(t, ValidateProcessOverrides(context.Background(), kb, &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 80},
	}, []string{"txt"}))
}
