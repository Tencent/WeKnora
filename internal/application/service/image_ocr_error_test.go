package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// errorVLM fails every prediction, standing in for a model endpoint that
// answered non-2xx: a 429 while the provider throttles, a 5xx, a timeout, or a
// 4xx such as 400/413/431 the endpoint rejected the request with.
type errorVLM struct{ err error }

func (m *errorVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	return "", m.err
}

func (m *errorVLM) PredictWithOptions(
	_ context.Context, _ [][]byte, _ string, _ *vlm.PredictOptions,
) (string, error) {
	return "", m.err
}

func (m *errorVLM) GetModelName() string { return "error" }
func (m *errorVLM) GetModelID() string   { return "error" }

var _ vlm.VLM = (*errorVLM)(nil)

// emptyVLM answers every prediction with no text: the call succeeded, the image
// simply carries nothing to transcribe.
type emptyVLM struct{}

func (emptyVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	return "", nil
}

func (emptyVLM) PredictWithOptions(
	_ context.Context, _ [][]byte, _ string, _ *vlm.PredictOptions,
) (string, error) {
	return "", nil
}

func (emptyVLM) GetModelName() string { return "empty" }
func (emptyVLM) GetModelID() string   { return "empty" }

var _ vlm.VLM = emptyVLM{}

func ocrRunContext(model vlm.VLM) *runContext {
	return &runContext{
		payload:    &types.ImageMultimodalPayload{ImageSourceType: "image"},
		model:      model,
		imageBytes: []byte("fake image bytes"),
		vlmCfg:     types.VLMConfig{},
		imageInfo:  &types.ImageInfo{},
		out:        types.JSONMap{},
	}
}

// TestRunOCRActionPropagatesModelError pins #4064: a failed OCR call must fail
// the action, not be recorded as an image that came out empty. The error the
// model returned is the one that travels out, so the task retries — the caller
// only stops hiding the failure, it never classifies it.
func TestRunOCRActionPropagatesModelError(t *testing.T) {
	sentinel := errors.New("429 too many requests")
	r := ocrRunContext(&errorVLM{err: sentinel})

	err := runOCRAction(context.Background(), r)
	if !errors.Is(err, sentinel) {
		t.Fatalf("runOCRAction error = %v, want the model error back", err)
	}
	if _, ok := r.out["ocr_error"]; !ok {
		t.Errorf("ocr_error not recorded for a failed call")
	}
	if _, skipped := r.out["ocr_skipped"]; skipped {
		t.Errorf(`a failed call must not read as skipped: %v`, r.out["ocr_skipped"])
	}
}

// TestRunOCRActionKeepsEmptyResultAnEmptyResult is the other half of the
// contract: a model that answered successfully with nothing to transcribe is
// not a failure, so the action still returns nil and marks the OCR skipped. The
// two must stay distinguishable, or a retry would replay a call that will keep
// answering the same way.
func TestRunOCRActionKeepsEmptyResultAnEmptyResult(t *testing.T) {
	r := ocrRunContext(emptyVLM{})

	if err := runOCRAction(context.Background(), r); err != nil {
		t.Fatalf("runOCRAction returned %v for an empty-but-successful answer", err)
	}
	if got := r.out["ocr_skipped"]; got != "empty_or_invalid" {
		t.Errorf("ocr_skipped = %v, want empty_or_invalid", got)
	}
	if _, ok := r.out["ocr_error"]; ok {
		t.Errorf("an empty answer must not be recorded as an error")
	}
}

// TestDefaultPipelinePropagatesOCRError is the #4064 regression at the level it
// was reported: the manual pipeline used to swallow the OCR failure, so
// processImage found neither caption nor text and finished the image as done.
// The pipeline must hand the model error straight up so the task is retried.
func TestDefaultPipelinePropagatesOCRError(t *testing.T) {
	sentinel := errors.New("503 service unavailable")
	payload := &types.ImageMultimodalPayload{
		ImageAttrsEnabled: false,
		ImagePipelineID:   types.ImagePipelineDefault,
	}
	pipeline := selectImagePipeline(payload)
	if pipeline == nil {
		t.Fatal("default pipeline not registered")
	}
	r := &runContext{
		payload:    payload,
		declared:   pipeline.Fields(),
		model:      &errorVLM{err: sentinel},
		imageBytes: []byte("fake image bytes"),
		vlmCfg:     types.VLMConfig{},
		imageInfo:  &types.ImageInfo{},
		out:        types.JSONMap{},
	}

	if err := pipeline.Run(context.Background(), r); !errors.Is(err, sentinel) {
		t.Fatalf("pipeline.Run error = %v, want the model error back", err)
	}
}
