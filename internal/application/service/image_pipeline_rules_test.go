package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestManualPipelineRejectsEveryActionOff covers the rule the manual pipeline
// declares: with its two action switches off there is nothing left to do to an
// image, and the run would record that rather than complain, so the rejection
// has to come from the check before it is stored.
func TestManualPipelineRejectsEveryActionOff(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	err := pipeline.Validate(map[string]any{
		manualCaptionKey: false,
		manualOCRKey:     false,
	})
	invalid, ok := err.(*types.ImagePipelineValidationError)
	if !ok {
		t.Fatalf("Validate(manual, both off) = %v, want an ImagePipelineValidationError", err)
	}
	if invalid.MessageKey == "" {
		t.Error("the rejection carries no message key, so the panel could not translate it")
	}
	// Pointing at a switch is what tells the user which one to turn back on.
	if invalid.Field != captionFieldKeyCaptionEnabled {
		t.Errorf("field = %q, want %q (the first of the pair)", invalid.Field, captionFieldKeyCaptionEnabled)
	}
}

// TestManualPipelineAcceptsOneActionOn is the other half: a knowledge base may
// run captions only, or OCR only, and a thinking switch that nobody touched must
// not be read as "off, therefore nothing to do".
func TestManualPipelineAcceptsOneActionOn(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	cases := []map[string]any{
		{manualCaptionKey: true, manualOCRKey: false},
		{manualCaptionKey: false, manualOCRKey: true},
		// Neither key carried: both default to true, which is what a knowledge
		// base saved before these fields existed gets.
		{},
		// A thinking switch off changes nothing about whether the actions run.
		{manualCaptionKey: true, manualOCRKey: true, manualCaptionThinkKey: false, manualOCRThinkKey: false},
		// Values that reached the pipeline through a form may arrive as strings.
		{manualCaptionKey: "true", manualOCRKey: "false"},
	}
	for i, params := range cases {
		if err := pipeline.Validate(params); err != nil {
			t.Errorf("case %d (%v) = %v, want nil", i, params, err)
		}
	}
}

// TestManualPipelineRejectsOffByValue pins that only the value decides, not the
// spelling: a false written as a string, or a zero, has to fail the same rule a
// boolean false does, or a caller posting JSON by hand slips past the check.
func TestManualPipelineRejectsOffByValue(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	cases := map[string]any{
		"boolean":  false,
		"string":   "false",
		"zero int": 0,
	}
	for _, caption := range cases {
		for _, ocr := range cases {
			err := pipeline.Validate(map[string]any{
				manualCaptionKey: caption,
				manualOCRKey:     ocr,
			})
			if err == nil {
				t.Errorf("both off as %#v and %#v = nil, want the rule to fire", caption, ocr)
			}
		}
	}
}

// TestUnknownPipelineIDSkipsValidation keeps a stored id this build does not
// ship out of blocking the save. The run falls back the same way, so a
// configuration that can no longer be reproduced must still be editable.
func TestUnknownPipelineIDSkipsValidation(t *testing.T) {
	err := ValidateImagePipelineParams(types.ImagePipelineID("pipeline_from_a_future_release"), map[string]any{})
	if err != nil {
		t.Errorf("Validate(unknown id) = %v, want nil", err)
	}
}

// TestValidationGoesThroughTheSavedPipeline pins that the check resolves the id
// the way the run does: a knowledge base that never named a pipeline is asking
// for the manual one, and its switches are still the ones under test.
func TestValidationGoesThroughTheSavedPipeline(t *testing.T) {
	if err := ValidateImagePipelineParams("", nil); err != nil {
		t.Errorf("Validate(\"\") = %v, want nil (both actions default on)", err)
	}
	err := ValidateImagePipelineParams(types.ImagePipelineDefault, map[string]any{
		manualCaptionKey: false,
		manualOCRKey:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "noActionEnabled") {
		t.Errorf("Validate(manual, both off) = %v, want the noActionEnabled message key", err)
	}
}

// Exercise the public validation path and the actual model calls, including
// legacy IDs, so accepted configurations cannot silently execute no actions.
func TestImagePipelineValidationMatchesExecution(t *testing.T) {
	cases := []struct {
		name      string
		params    map[string]any
		wantCalls int
	}{
		{"defaults", nil, 2},
		{"both off", map[string]any{manualCaptionKey: false, manualOCRKey: false}, 0},
		{"caption only", map[string]any{manualCaptionKey: true, manualOCRKey: false}, 1},
		{"ocr only", map[string]any{manualCaptionKey: false, manualOCRKey: true}, 1},
		{
			"another pipeline's switches are not this pipeline's",
			map[string]any{"smartocr.caption_enabled": false, "smartocr.ocr_enabled": false},
			2,
		},
		{
			"a key with no slot is not a home",
			map[string]any{"caption_enabled": false, "ocr_enabled": false},
			2,
		},
		{"a null value uses the declared default", map[string]any{manualCaptionKey: nil, manualOCRKey: false}, 1},
		{"string true", map[string]any{manualCaptionKey: "true", manualOCRKey: false}, 1},
		{"string on", map[string]any{manualCaptionKey: "on", manualOCRKey: false}, 1},
		{"string one", map[string]any{manualCaptionKey: "1", manualOCRKey: false}, 1},
		{"string false", map[string]any{manualCaptionKey: "false", manualOCRKey: false}, 0},
		{
			"numeric values are not boolean switches",
			map[string]any{manualCaptionKey: float64(1), manualOCRKey: false},
			0,
		},
	}
	for _, id := range []types.ImagePipelineID{types.ImagePipelineDefault, types.ImagePipelineLegacyCaptionOCR, ""} {
		for _, tc := range cases {
			t.Run(string(id)+"/"+tc.name, func(t *testing.T) {
				enabled := true
				overrides := &types.KnowledgeProcessOverrides{
					EnableMultimodel: &enabled, ImagePipelineID: &id,
					ImagePipelineParams: tc.params,
				}
				validationErr := ValidateProcessOverrides(
					context.Background(), &types.KnowledgeBase{}, overrides, []string{"pdf"})
				pipelineErr := defaultPipeline{}.Validate(tc.params)
				if tc.wantCalls == 0 {
					require.ErrorContains(t, validationErr, "noActionEnabled")
					require.ErrorContains(t, pipelineErr, "noActionEnabled")
					return
				}
				require.NoError(t, validationErr)
				require.NoError(t, pipelineErr)
				repo := &attrsChunkRepo{}
				svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
				model := &stubVLM{}
				out := types.JSONMap{}
				err := svc.processImage(context.Background(), &types.ImageMultimodalPayload{
					ImageURL: "local://test.png", ImagePipelineID: id, ImagePipelineParams: tc.params,
				}, model, types.VLMConfig{}, noopSpanTracker{}, out)
				require.NoError(t, err)
				require.Len(t, model.prompts, tc.wantCalls)
				require.Len(t, repo.created, tc.wantCalls)
			})
		}
	}
}
