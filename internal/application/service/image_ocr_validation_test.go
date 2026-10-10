package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type ocrTestTracker struct {
	noopSpanTracker
	failed  map[string]string
	outputs map[string]types.JSONMap
}

func (t *ocrTestTracker) LookupStage(context.Context, string, int, string) *Span {
	return &Span{Name: "multimodal"}
}

func (t *ocrTestTracker) BeginSubSpan(_ context.Context, _ *Span, name, _ string, _ types.JSONMap) *Span {
	return &Span{Name: name}
}

func (t *ocrTestTracker) FailSpan(_ context.Context, s *Span, code, _ string, _ error) {
	t.failed[s.Name] = code
}

func (t *ocrTestTracker) EndSpan(_ context.Context, s *Span, out types.JSONMap) {
	t.outputs[s.Name] = out
}

func TestImageOCRRejectsInvalidContentBeforePersistence(t *testing.T) {
	for _, tc := range []struct {
		name, raw, status, code string
		err                     error
	}{
		{name: "recorded empty cells", raw: strings.Repeat("|  ", 2500), status: "failed", code: "OCR_INVALID_OUTPUT"},
		{name: "repeated words", raw: strings.Repeat("传球按钮 ", 200), status: "failed", code: "OCR_INVALID_OUTPUT"},
		{name: "empty model output", status: "failed", code: "OCR_INVALID_OUTPUT"},
		{
			name: "truncated output", status: "failed", code: "OCR_TRUNCATED",
			err: fmt.Errorf("request: %w", vlm.ErrTruncatedCompletion),
		},
		{name: "request failure", status: "failed", code: "OCR_REQUEST_FAILED", err: errors.New("model unavailable")},
		{name: "no text", raw: "No text content.", status: "no_text"},
	} {
		for _, caption := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/caption=%v", tc.name, caption), func(t *testing.T) {
				repo := &attrsChunkRepo{}
				svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
				model := &attrsFakeVLM{reply: func(prompt string, _ int) (string, error) {
					if prompt == vlmOCRPrompt {
						return tc.raw, tc.err
					}
					return "A game screenshot.", nil
				}}
				tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
				out := types.JSONMap{}
				err := svc.processImage(context.Background(), &types.ImageMultimodalPayload{
					ImageURL: "local://test.png", EnableOCR: true, EnableCaption: caption, Attempt: 1,
				}, model, types.VLMConfig{}, tracker, out)
				require.NoError(t, err, "bad OCR must not retry the whole image and duplicate a valid caption")
				require.Equal(t, tc.status, out["ocr_status"])
				require.Equal(t, 0, out["ocr_chars"])
				require.NotContains(t, out, "ocr_preview")
				require.Equal(t, tc.code, tracker.failed["multimodal.image[0].ocr"])
				if tc.code != "" {
					require.Equal(t, tc.code, out["ocr_error_code"])
					require.NotEmpty(t, out["ocr_error"])
					require.NotContains(t, tracker.outputs, "multimodal.image[0].ocr",
						"failed OCR must not be marked done")
					if caption {
						require.Equal(t, "partial_failure", out["outcome"])
					} else {
						require.Equal(t, "failed", out["outcome"])
					}
				} else {
					require.NotContains(t, out, "ocr_error")
					require.Equal(t, "no_text", tracker.outputs["multimodal.image[0].ocr"]["status"])
				}
				if caption {
					require.Len(t, repo.created, 1)
				} else {
					require.Empty(t, repo.created)
				}
				for _, chunk := range repo.created {
					require.Equal(t, types.ChunkTypeImageCaption, chunk.ChunkType)
					require.NotContains(t, chunk.ImageInfo, "|  |")
					require.NotContains(t, chunk.ImageInfo, "传球按钮")
				}
			})
		}
	}
}
