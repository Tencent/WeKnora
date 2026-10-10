package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Opt-in verification against the configured VLM, with production processing
// and an in-memory chunk repository. No knowledge-base records are modified.
// Fixture: {"model": <Model>, "image_paths": [<local path>, ...]}.
// Keep credentials and user images outside version control; private endpoints
// require the usual explicit SSRF_WHITELIST environment setting.
func TestLiveImageOCRValidation(t *testing.T) {
	path := os.Getenv("WEKNORA_OCR_TEST_FIXTURE")
	if path == "" {
		t.Skip("set WEKNORA_OCR_TEST_FIXTURE to run live OCR verification")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture struct {
		Model      types.Model `json:"model"`
		ImagePaths []string    `json:"image_paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture.ImagePaths)
	model, err := vlm.NewRemoteAPIVLM(vlm.ConfigFromModel(&fixture.Model, "", ""))
	require.NoError(t, err)
	for _, path := range fixture.ImagePaths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			repo := &attrsChunkRepo{}
			svc := newAttrsTestService(&attrsFileService{body: data}, repo)
			tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
			out := types.JSONMap{}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			start := time.Now()
			err = svc.processImage(ctx, &types.ImageMultimodalPayload{
				ImageURL: "local://fixture", EnableOCR: true, Attempt: 1,
			}, model, types.VLMConfig{}, tracker, out)
			require.NoError(t, err)
			require.Empty(t, tracker.failed)
			require.Equal(t, "succeeded", out["ocr_status"], "OCR outcome: %v", out)
			require.Len(t, repo.created, 1)
			chunk := repo.created[0]
			require.Equal(t, types.ChunkTypeImageOCR, chunk.ChunkType)
			require.NotEmpty(t, chunk.Content)
			t.Logf("duration=%s chars=%d status=%s preview=%q", time.Since(start), len([]rune(chunk.Content)),
				out["ocr_status"], previewText(chunk.Content, 180))
		})
	}
}
