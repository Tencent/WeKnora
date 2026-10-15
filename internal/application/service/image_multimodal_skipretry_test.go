package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestIsPermanentVLMFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("boom"), false},
		{"context deadline", context.DeadlineExceeded, false},
		{"context cancelled", context.Canceled, false},
		{"401 permanent", &api.HTTPError{StatusCode: 401}, true},
		{"403 permanent", &api.HTTPError{StatusCode: 403}, true},
		{"404 permanent", &api.HTTPError{StatusCode: 404}, true},
		{"400 permanent", &api.HTTPError{StatusCode: 400}, true},
		{"413 permanent", &api.HTTPError{StatusCode: 413}, true},
		{"429 retryable", &api.HTTPError{StatusCode: 429}, false},
		{"408 retryable", &api.HTTPError{StatusCode: 408}, false},
		{"500 retryable", &api.HTTPError{StatusCode: 500}, false},
		{"503 retryable", &api.HTTPError{StatusCode: 503}, false},
		{
			"transport send-request down",
			&api.TransportError{Op: "send request", Err: errors.New("connection refused")}, true,
		},
		{
			"transport read-response transient",
			&api.TransportError{Op: "read response", Err: errors.New("unexpected EOF")}, false,
		},
		// The manager wraps the original api error with %w, so the underlying
		// api error must still be detectable.
		{
			"wrapped send-request",
			fmt.Errorf("endpoint down: %w", &api.TransportError{Op: "send request", Err: errors.New("i/o timeout")}),
			true,
		},
		{"wrapped 401", fmt.Errorf("verdict: %w", &api.HTTPError{StatusCode: 401}), true},
		{"wrapped 500", fmt.Errorf("verdict: %w", &api.HTTPError{StatusCode: 500}), false},
	}
	for _, tc := range cases {
		if got := isPermanentVLMFailure(tc.err); got != tc.want {
			t.Errorf("%s: isPermanentVLMFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWrapSkipRetry(t *testing.T) {
	// Permanent failures get the asynq.SkipRetry sentinel and keep the original
	// error inspectable.
	down := wrapSkipRetry(&api.TransportError{Op: "send request", Err: errors.New("connection refused")})
	if !errors.Is(down, asynq.SkipRetry) {
		t.Errorf("down error = %v, want it to carry asynq.SkipRetry", down)
	}
	var te *api.TransportError
	if !errors.As(down, &te) {
		t.Errorf("down error = %v, want the original TransportError still inspectable", down)
	}

	perm := wrapSkipRetry(&api.HTTPError{StatusCode: 401})
	if !errors.Is(perm, asynq.SkipRetry) {
		t.Errorf("perm error = %v, want asynq.SkipRetry", perm)
	}

	// Retryable failures are returned unchanged (errors.Is must be false).
	retry := wrapSkipRetry(&api.HTTPError{StatusCode: 500})
	if errors.Is(retry, asynq.SkipRetry) {
		t.Errorf("retry(500) error = %v, want NO asynq.SkipRetry", retry)
	}
	retry2 := wrapSkipRetry(&api.TransportError{Op: "read response", Err: errors.New("EOF")})
	if errors.Is(retry2, asynq.SkipRetry) {
		t.Errorf("retry(read-response) error = %v, want NO asynq.SkipRetry", retry2)
	}

	if got := wrapSkipRetry(nil); got != nil {
		t.Errorf("wrapSkipRetry(nil) = %v, want nil", got)
	}
}

// TestAllFailedImageIsAnError pins the #4132 refinement: a run that produced
// NOTHING (no caption, no OCR) and had every VLM call fail is a real failure —
// the image would be invisible in the gallery, so a silent "skipped" success
// would hide a dead endpoint behind a green trace. A run that DID produce a
// caption keeps the recorded-not-propagated semantics.
func TestAllFailedImageIsAnError(t *testing.T) {
	newDown := func() *attrsFakeVLM {
		return &attrsFakeVLM{reply: func(string, int) (string, error) {
			return "", &api.TransportError{Op: "send request", Err: errors.New("connection refused")}
		}}
	}

	t.Run("caption off + ocr failed = error", func(t *testing.T) {
		repo := &attrsChunkRepo{}
		svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
		tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
		out := types.JSONMap{}
		err := svc.processImage(context.Background(), &types.ImageMultimodalPayload{
			ImageURL:            "local://test.png",
			ImagePipelineParams: map[string]any{"enable_caption": false, "enable_ocr": true},
			Attempt:             1,
		}, newDown(), types.VLMConfig{}, tracker, out)
		require.Error(t, err, "nothing was produced and every call failed: the image must not pass as skipped")
		// The typed VLM error must survive the wrap so Handle's wrapSkipRetry
		// can classify the run (covered by TestWrapSkipRetry — this test
		// calls processImage directly, before that wrap).
		var te *api.TransportError
		require.ErrorAs(t, err, &te)
		require.Equal(t, "failed", out["outcome"])
		require.Equal(t, "MULTIMODAL_VLM_FAILED", tracker.failed["multimodal.image[0]"],
			"the image span must close red")
	})

	t.Run("caption on + ocr failed = recorded, not propagated", func(t *testing.T) {
		repo := &attrsChunkRepo{}
		svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
		model := &attrsFakeVLM{reply: func(prompt string, _ int) (string, error) {
			if prompt == vlmOCRPrompt {
				return "", &api.TransportError{Op: "send request", Err: errors.New("connection refused")}
			}
			return "A game screenshot.", nil
		}}
		tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
		out := types.JSONMap{}
		err := svc.processImage(context.Background(), &types.ImageMultimodalPayload{
			ImageURL:            "local://test.png",
			ImagePipelineParams: map[string]any{"enable_caption": true, "enable_ocr": true},
			Attempt:             1,
		}, model, types.VLMConfig{}, tracker, out)
		require.NoError(t, err, "a valid caption must not be duplicated by an OCR retry")
		require.Equal(t, "partial_failure", out["outcome"])
	})
}

// TestCancelledAllFailedImageIsCancelled pins the cancellation distinction: a
// run walked away from with nothing produced closes its span
// MULTIMODAL_CANCELLED, and the error does NOT carry asynq.SkipRetry (the
// executor is revoking the task anyway; the classification must not pretend
// the endpoint is permanent).
func TestCancelledAllFailedImageIsCancelled(t *testing.T) {
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
	model := &attrsFakeVLM{reply: func(string, int) (string, error) {
		return "", errors.New("model unavailable")
	}}
	tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
	out := types.JSONMap{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.processImage(ctx, &types.ImageMultimodalPayload{
		ImageURL:            "local://test.png",
		ImagePipelineParams: map[string]any{"enable_caption": false, "enable_ocr": true},
		Attempt:             1,
	}, model, types.VLMConfig{}, tracker, out)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, asynq.SkipRetry)
	require.Equal(t, "MULTIMODAL_CANCELLED", tracker.failed["multimodal.image[0]"])
}
