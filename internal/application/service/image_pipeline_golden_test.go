package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubVLM answers observation prompts with a well-formed observation (a block of
// text, so the policy wants OCR) and every other prompt with prose. It records
// each prompt so a test can tell how many model calls a pipeline spent.
type stubVLM struct{ prompts []string }

func (s *stubVLM) Predict(_ context.Context, _ [][]byte, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if strings.Contains(prompt, "Observe the following") {
		return "contain.text: block\ncontain.data_visual: true\nDESCRIPTION: a stubbed image\n", nil
	}
	return "stubbed answer\n", nil
}

func (s *stubVLM) GetModelName() string { return "stub" }
func (s *stubVLM) GetModelID() string   { return "stub" }

var _ vlm.VLM = (*stubVLM)(nil)

// runOneImagePipeline drives a whole pipeline for one image, exactly as
// processImage does, and returns the run context plus the model it talked to.
func runOneImagePipeline(t *testing.T, attrsEnabled, enableCaption, enableOCR bool) (*runContext, *stubVLM) {
	t.Helper()
	model := &stubVLM{}
	r := &runContext{
		payload: &types.ImageMultimodalPayload{
			ImageAttrsEnabled: attrsEnabled,
			EnableCaption:     enableCaption,
			EnableOCR:         enableOCR,
		},
		model:      model,
		imageBytes: []byte("fake image bytes"),
		vlmCfg:     types.VLMConfig{},
		imageInfo:  &types.ImageInfo{},
		out:        types.JSONMap{},
	}
	pipeline := selectImagePipeline(attrsEnabled)
	if pipeline == nil {
		t.Fatalf("no pipeline registered for attrsEnabled=%v", attrsEnabled)
	}
	if err := pipeline.Run(context.Background(), r); err != nil {
		t.Fatalf("pipeline %s returned an error: %v", pipeline.ID(), err)
	}
	return r, model
}

// TestImagePipelineActionSequences pins the eight combinations of the two
// whole-task switches onto the two pipelines. The sequence is the per-image
// answer to "what did this image go through", so it is what a reader of a trace
// replays when an image came out wrong.
func TestImagePipelineActionSequences(t *testing.T) {
	cases := []struct {
		name string
		// attrs / caption / ocr are the payload switches
		attrs, caption, ocr bool
		pipeline            types.ImagePipelineID
		want                []types.ImageActionID
		wantVLMCalls        int
	}{
		{"observation, caption and OCR", true, true, true, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption, types.ImageActionOCR}, 2},
		{"observation and caption", true, true, false, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption}, 1},
		{"observation and OCR, caption off", true, false, true, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption, types.ImageActionOCR}, 2},
		{"observation only", true, false, false, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption}, 1},
		{"caption and OCR", false, true, true, types.ImagePipelineCaptionOCR,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR}, 2},
		{"caption only", false, true, false, types.ImagePipelineCaptionOCR,
			[]types.ImageActionID{types.ImageActionCaption}, 1},
		{"OCR only", false, false, true, types.ImagePipelineCaptionOCR,
			[]types.ImageActionID{types.ImageActionOCR}, 1},
		{"nothing at all", false, false, false, types.ImagePipelineCaptionOCR,
			nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, model := runOneImagePipeline(t, tc.attrs, tc.caption, tc.ocr)
			if got := selectImagePipeline(tc.attrs).ID(); got != tc.pipeline {
				t.Errorf("pipeline = %q, want %q", got, tc.pipeline)
			}
			if len(r.actions) != len(tc.want) {
				t.Fatalf("actions = %v, want %v", r.actions, tc.want)
			}
			for i, want := range tc.want {
				if r.actions[i] != want {
					t.Errorf("action[%d] = %q, want %q", i, r.actions[i], want)
				}
			}
			if len(model.prompts) != tc.wantVLMCalls {
				t.Errorf("VLM calls = %d (%v), want %d", len(model.prompts), model.prompts, tc.wantVLMCalls)
			}
		})
	}
}

// TestObservationCaptionSharesOneCall is the point of the whole decomposition:
// with attributes on, describing and observing is one action and therefore one
// model request, and the standalone caption action is never scheduled.
func TestObservationCaptionSharesOneCall(t *testing.T) {
	r, model := runOneImagePipeline(t, true, true, false)
	if len(model.prompts) != 1 {
		t.Fatalf("VLM calls = %d (%v), want 1 — observation and caption must share a request",
			len(model.prompts), model.prompts)
	}
	if !strings.Contains(model.prompts[0], "Observe the following") {
		t.Errorf("the single prompt is not the observation prompt: %q", model.prompts[0])
	}
	for _, id := range r.actions {
		if id == types.ImageActionCaption {
			t.Error("the standalone caption action ran; observation must carry the caption")
		}
	}
	// The shared request also fills the caption slot, which is the other half of
	// "observation carries caption".
	if got := r.imageInfo.Caption; got != "a stubbed image" {
		t.Errorf("Caption = %q, want the description from the same call", got)
	}
	if _, ok := r.out["attr_policy"]; !ok {
		t.Error(`out["attr_policy"] is missing; the trace must state the decision`)
	}
}

// TestObservationPipelineTrace pins what a trace row says when OCR is skipped by
// the attribute policy, which is the case image_info alone cannot explain.
func TestObservationPipelineTrace(t *testing.T) {
	r, _ := runOneImagePipeline(t, true, true, false)
	if got := r.out["ocr_skipped"]; got != "attr_policy" {
		t.Errorf(`out["ocr_skipped"] = %v, want "attr_policy"`, got)
	}
	if _, ok := r.out["ocr_text"]; ok {
		t.Error("no OCR action ran, so no ocr slot may exist")
	}
}

// TestCaptionOcrTrace pins the plain path: both actions run and no policy field
// is invented, because there is nothing to decide from.
func TestCaptionOcrTrace(t *testing.T) {
	r, _ := runOneImagePipeline(t, false, true, true)
	if r.imageInfo.Caption == "" {
		t.Error("caption slot is empty after the caption action")
	}
	if r.imageInfo.OCRText == "" {
		t.Error("OCR slot is empty after the OCR action")
	}
	if _, ok := r.out["attr_policy"]; ok {
		t.Error("caption+OCR mode has no attributes, so it must not record a policy")
	}
	if _, ok := r.out["ocr_skipped"]; ok {
		t.Errorf("unexpected ocr_skipped in the plain path: %v", r.out["ocr_skipped"])
	}
}

// TestImagePipelineIDsAreStable pins the two ids and the strings that reach the
// trace — the labels a reader greps for.
func TestImagePipelineIDsAreStable(t *testing.T) {
	if got := types.ImagePipelineIDFor(true); got != types.ImagePipelineObCapOCR {
		t.Errorf("ImagePipelineIDFor(true) = %q", got)
	}
	if got := string(types.ImagePipelineObCapOCR); got != "ob_cap_ocr" {
		t.Errorf(`pipeline id = %q, want "ob_cap_ocr"`, got)
	}
	if got := string(types.ImagePipelineCaptionOCR); got != "caption_ocr" {
		t.Errorf(`pipeline id = %q, want "caption_ocr"`, got)
	}
}

// TestImageActionRegistryCoversSharedActions makes sure every action declared in
// types is actually runnable: a declaration without a binding would surface as a
// silently skipped field instead of a build error.
func TestImageActionRegistryCoversSharedActions(t *testing.T) {
	if len(types.SharedImageActions) == 0 {
		t.Fatal("SharedImageActions is empty")
	}
	for _, spec := range types.SharedImageActions {
		registered, ok := imageActionRegistry[spec.ID]
		if !ok {
			t.Errorf("action %q is declared but never registered", spec.ID)
			continue
		}
		if registered.handler == nil {
			t.Errorf("action %q is registered without a handler", spec.ID)
		}
	}
}

// TestExecuteSkipsUnimplementedAction keeps the registry's failure mode honest:
// one missing action costs one field, it does not abort the image.
func TestExecuteSkipsUnimplementedAction(t *testing.T) {
	r := &runContext{out: types.JSONMap{}, imageInfo: &types.ImageInfo{}}
	err := r.execute(context.Background(), types.ImageActionID("not_registered"))
	if err != nil {
		t.Fatalf("execute returned %v, want nil", err)
	}
	if got := r.out["action_skipped"]; got != "not_registered" {
		t.Errorf(`out["action_skipped"] = %v, want "not_registered"`, got)
	}
}
