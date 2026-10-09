package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestManualPipelineRejectsEveryActionOff covers the rule the manual pipeline
// declares: with its two action switches off there is nothing left to do to an
// image, and the run would record that rather than complain, so the rejection
// has to come from the check before it is stored.
func TestManualPipelineRejectsEveryActionOff(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	err := pipeline.Validate(map[string]any{
		captionFieldKeyEnableCaption: false,
		captionFieldKeyEnableOCR:     false,
	})
	invalid, ok := err.(*types.ImagePipelineValidationError)
	if !ok {
		t.Fatalf("Validate(manual, both off) = %v, want an ImagePipelineValidationError", err)
	}
	if invalid.MessageKey == "" {
		t.Error("the rejection carries no message key, so the panel could not translate it")
	}
	// Pointing at a switch is what tells the user which one to turn back on.
	if invalid.Field != captionFieldKeyEnableCaption {
		t.Errorf("field = %q, want %q (the first of the pair)", invalid.Field, captionFieldKeyEnableCaption)
	}
}

// TestManualPipelineAcceptsOneActionOn is the other half: a knowledge base may
// run captions only, or OCR only, and a thinking switch that nobody touched must
// not be read as "off, therefore nothing to do".
func TestManualPipelineAcceptsOneActionOn(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	cases := []map[string]any{
		{captionFieldKeyEnableCaption: true, captionFieldKeyEnableOCR: false},
		{captionFieldKeyEnableCaption: false, captionFieldKeyEnableOCR: true},
		// Neither key carried: both default to true, which is what a knowledge
		// base saved before these fields existed gets.
		{},
		// A thinking switch off changes nothing about whether the actions run.
		{
			captionFieldKeyEnableCaption: true, captionFieldKeyEnableOCR: true,
			imageFieldKeyCaptionThinking: false, imageFieldKeyOCRThinking: false,
		},
		// Values that reached the pipeline through a form may arrive as strings.
		{captionFieldKeyEnableCaption: "true", captionFieldKeyEnableOCR: "false"},
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
				captionFieldKeyEnableCaption: caption,
				captionFieldKeyEnableOCR:     ocr,
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
		captionFieldKeyEnableCaption: false,
		captionFieldKeyEnableOCR:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "noActionEnabled") {
		t.Errorf("Validate(manual, both off) = %v, want the noActionEnabled message key", err)
	}
}
