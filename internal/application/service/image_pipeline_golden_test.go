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
// params are the pipeline's private tunables, as the knowledge base would have
// resolved them; an empty map means "whatever the pipeline defaults to".
func runOneImagePipeline(t *testing.T, attrsEnabled bool, params map[string]any) (*runContext, *stubVLM) {
	t.Helper()
	model := &stubVLM{}
	payload := &types.ImageMultimodalPayload{
		ImageAttrsEnabled:   attrsEnabled,
		ImagePipelineID:     types.ImagePipelineIDFor(attrsEnabled),
		ImagePipelineParams: params,
	}
	pipeline := selectImagePipeline(payload)
	if pipeline == nil {
		t.Fatalf("no pipeline registered for attrsEnabled=%v", attrsEnabled)
	}
	r := &runContext{
		payload:    payload,
		params:     params,
		declared:   pipeline.Fields(),
		model:      model,
		imageBytes: []byte("fake image bytes"),
		vlmCfg:     types.VLMConfig{},
		imageInfo:  &types.ImageInfo{},
		out:        types.JSONMap{},
	}
	if err := pipeline.Run(context.Background(), r); err != nil {
		t.Fatalf("pipeline %s returned an error: %v", pipeline.ID(), err)
	}
	return r, model
}

// TestImagePipelineActionSequences pins how the private switches of each
// pipeline map onto the actions that run. The sequence is the per-image answer to
// "what did this image go through", so it is what a reader of a trace replays
// when an image came out wrong.
//
// The two pipelines own different keys on purpose: the default pipeline asks
// whether to run a step, ob_cap_ocr asks whether OCR may be spent at all. Both
// default to on, which is what an untouched knowledge base gets.
func TestImagePipelineActionSequences(t *testing.T) {
	cases := []struct {
		name string
		// attrs picks the pipeline; the other two are that pipeline's fields.
		attrs        bool
		caption, ocr bool
		pipeline     types.ImagePipelineID
		want         []types.ImageActionID
		wantVLMCalls int
	}{
		{
			"observation, caption and OCR", true, true, true, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption, types.ImageActionOCR},
			2,
		},
		{
			"observation and caption", true, false, true, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption, types.ImageActionOCR},
			2,
		},
		{
			"observation, caption kept", true, true, false, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption},
			1,
		},
		{
			"observation, caption dropped", true, false, false, types.ImagePipelineObCapOCR,
			[]types.ImageActionID{types.ImageActionObservationCaption},
			1,
		},
		{
			"caption and OCR", false, true, true, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR},
			2,
		},
		{
			"caption only", false, true, false, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption},
			1,
		},
		{
			"OCR only", false, false, true, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionOCR},
			1,
		},
		{
			"nothing at all", false, false, false, types.ImagePipelineDefault,
			nil, 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{
				obFieldKeyCaptureCaption:     tc.caption,
				obFieldKeyAllowOCR:           tc.ocr,
				captionFieldKeyEnableCaption: tc.caption,
				captionFieldKeyEnableOCR:     tc.ocr,
			}
			if !tc.attrs {
				// The observed pipeline ignores the default pipeline's keys and
				// vice versa; each row only sets the ones its pipeline declares.
				params = map[string]any{
					captionFieldKeyEnableCaption: tc.caption,
					captionFieldKeyEnableOCR:     tc.ocr,
				}
			}
			r, model := runOneImagePipeline(t, tc.attrs, params)
			if got := selectImagePipeline(r.payload).ID(); got != tc.pipeline {
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

// TestPipelineFieldsArePrivate is the contract the settings panel rests on: no
// two pipelines may declare the same field key, because the knowledge base
// stores them per pipeline and a collision would make one pipeline's panel
// control another's run.
func TestPipelineFieldsArePrivate(t *testing.T) {
	seen := make(map[string]types.ImagePipelineID)
	for _, spec := range ListImagePipelines() {
		keys := make(map[string]bool)
		for _, field := range spec.Fields {
			if keys[field.Key] {
				t.Errorf("pipeline %q declares field %q twice", spec.ID, field.Key)
			}
			keys[field.Key] = true
			if field.Key == "" {
				t.Errorf("pipeline %q declares a field with an empty key", spec.ID)
				continue
			}
			if field.Default == nil {
				t.Errorf("field %q of pipeline %q has no default, so the panel would show an unset toggle",
					field.Key, spec.ID)
			}
			if field.Type != types.ImageFieldTypeBool &&
				field.Type != types.ImageFieldTypeString &&
				field.Type != types.ImageFieldTypeEnum {
				t.Errorf("field %q of pipeline %q has an unknown type %q", field.Key, spec.ID, field.Type)
			}
			if other, dup := seen[field.Key]; dup && other != spec.ID {
				t.Errorf("field %q is declared by both %q and %q; pipeline fields must not overlap",
					field.Key, other, spec.ID)
			}
			seen[field.Key] = spec.ID
		}
	}
}

// TestListImagePipelinesMatchesRegistration pins that the endpoint's payload is
// the registry itself: a pipeline registered but missing from the list, or
// listed without the fields it declares, would leave the panel rendering a
// control that the run never reads.
func TestListImagePipelinesMatchesRegistration(t *testing.T) {
	specs := ListImagePipelines()
	if len(specs) < 2 {
		t.Fatalf("list = %d pipelines, want at least the two registered", len(specs))
	}
	for _, spec := range specs {
		registered, ok := imagePipelineRegistry[spec.ID]
		if !ok {
			t.Fatalf("list carries pipeline %q, which is not registered", spec.ID)
		}
		if spec.Name != registered.Name() {
			t.Errorf("name of %q = %q, want %q", spec.ID, spec.Name, registered.Name())
		}
		if got := len(spec.Fields); got != len(registered.Fields()) {
			t.Errorf("fields of %q = %d, want %d", spec.ID, got, len(registered.Fields()))
		}
	}
}

// TestParamFallsBackToDeclaredDefault pins that a knowledge base which never
// set a tunable still runs with the pipeline's default rather than a zero value:
// an unset map would otherwise silently mean "off" for every switch.
func TestParamFallsBackToDeclaredDefault(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	r := &runContext{out: types.JSONMap{}, imageInfo: &types.ImageInfo{}, declared: pipeline.Fields()}
	if got := r.BoolParam(captionFieldKeyEnableOCR); got != true {
		t.Errorf("BoolParam(%q) with no params = %v, want true (the declared default)",
			captionFieldKeyEnableOCR, got)
	}
	// A key this pipeline never declared reads nil rather than panicking: an old
	// stored config may carry parameters for a pipeline that has since changed.
	if got := r.Param("no_such_field"); got != nil {
		t.Errorf("Param(\"no_such_field\") = %v, want nil", got)
	}
}

// TestObservationPipelineDeclaresNoControls pins the panel contract of
// ob_cap_ocr: the model decides from the observed attributes, so the pipeline
// declares no fields and the panel renders nothing for it. Its two tunables
// remain readable as parameters for an API caller, defaulting to true — the
// behaviour an untouched knowledge base has always had.
func TestObservationPipelineDeclaresNoControls(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineObCapOCR]
	if fields := pipeline.Fields(); len(fields) != 0 {
		t.Errorf("ob_cap_ocr declares %d fields, want none — the panel must not offer manual switches",
			len(fields))
	}
	r := &runContext{out: types.JSONMap{}, imageInfo: &types.ImageInfo{}, declared: pipeline.Fields()}
	if !r.BoolParamOr(obFieldKeyAllowOCR, true) {
		t.Error("allow_ocr with no params must default to true")
	}
	r.params = map[string]any{obFieldKeyAllowOCR: false}
	if r.BoolParamOr(obFieldKeyAllowOCR, true) {
		t.Error("an explicit allow_ocr=false must override the pinned default")
	}
}

// TestUnknownPipelineFieldIsIgnored keeps a stale stored parameter harmless. A
// knowledge base may carry a key the running pipeline no longer declares; it
// must not leak into the trace snapshot nor change the run.
func TestUnknownPipelineFieldIsIgnored(t *testing.T) {
	r, model := runOneImagePipeline(t, false, map[string]any{
		captionFieldKeyEnableCaption: true,
		captionFieldKeyEnableOCR:     true,
		"removed_by_a_later_release": true,
	})
	if len(model.prompts) != 2 {
		t.Fatalf("VLM calls = %d (%v), want 2", len(model.prompts), model.prompts)
	}
	if _, leaked := r.out["params"].(types.JSONMap)["removed_by_a_later_release"]; leaked {
		t.Error("an undeclared key must not reach the trace snapshot")
	}
}

// TestObservationCaptionSharesOneCall is the point of the whole decomposition:
// with attributes on, describing and observing is one action and therefore one
// model request, and the standalone caption action is never scheduled.
func TestObservationCaptionSharesOneCall(t *testing.T) {
	r, model := runOneImagePipeline(t, true, map[string]any{
		obFieldKeyCaptureCaption: true,
		obFieldKeyAllowOCR:       false,
	})
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
	r, _ := runOneImagePipeline(t, true, map[string]any{
		obFieldKeyCaptureCaption: true,
		obFieldKeyAllowOCR:       false,
	})
	if got := r.out["ocr_skipped"]; got != "attr_policy" {
		t.Errorf(`out["ocr_skipped"] = %v, want "attr_policy"`, got)
	}
	if _, ok := r.out["ocr_text"]; ok {
		t.Error("no OCR action ran, so no ocr slot may exist")
	}
}

// TestDefaultPipelineTrace pins the plain path: both actions run and no policy
// field is invented, because there is nothing to decide from.
func TestDefaultPipelineTrace(t *testing.T) {
	r, _ := runOneImagePipeline(t, false, map[string]any{
		captionFieldKeyEnableCaption: true,
		captionFieldKeyEnableOCR:     true,
	})
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
	if got := string(types.ImagePipelineDefault); got != "default" {
		t.Errorf(`pipeline id = %q, want "default"`, got)
	}
	if got := string(types.NormalizeImagePipelineID(types.ImagePipelineLegacyCaptionOCR)); got != "default" {
		t.Errorf(`legacy id normalized = %q, want "default"`, got)
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
