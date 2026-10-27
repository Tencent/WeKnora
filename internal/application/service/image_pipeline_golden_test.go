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

// PredictWithOptions forwards to Predict: the stub records prompts, so the
// switch the pipeline asked for is not part of what it pins.
func (s *stubVLM) PredictWithOptions(
	_ context.Context, _ [][]byte, prompt string, _ *vlm.PredictOptions,
) (string, error) {
	return s.Predict(context.Background(), nil, prompt)
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
	// No policy: the payload carries none, and DecideOCR falls back to the
	// built-in table — the same conservative policy the field promises.
	return runImagePipeline(t, attrsEnabled, params, types.ImageActionsConfig{})
}

// runImagePipeline is runOneImagePipeline with the attribute policy spelled out.
// The policy is the only thing that decides whether OCR runs (there is no ceiling
// switch any more), so tests whose subject is something else use a policy that
// declines it.
func runImagePipeline(
	t *testing.T, attrsEnabled bool, params map[string]any, actions types.ImageActionsConfig,
) (*runContext, *stubVLM) {
	t.Helper()
	model := &stubVLM{}
	payload := &types.ImageMultimodalPayload{
		ImageAttrsEnabled:   attrsEnabled,
		ImagePipelineID:     types.ImagePipelineIDFor(attrsEnabled),
		ImagePipelineParams: params,
		ImageActions:        actions,
	}
	pipeline := selectImagePipeline(payload)
	if pipeline == nil {
		t.Fatalf("no pipeline registered for attrsEnabled=%v", attrsEnabled)
	}
	r := &runContext{
		payload:    payload,
		pipelineID: string(pipeline.ID()),
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

// The switch keys as they are stored: each under the slot of the pipeline that
// declares it (types.SlotKey). Tests build configs the way the settings panel
// builds them — a bare key is a spelling no client produces, so a test that used
// one was asserting against a config that cannot be saved.
var (
	manualCaptionKey      = types.SlotKey(string(types.ImagePipelineDefault), captionFieldKeyCaptionEnabled)
	manualOCRKey          = types.SlotKey(string(types.ImagePipelineDefault), captionFieldKeyOCREnabled)
	manualCaptionThinkKey = types.SlotKey(string(types.ImagePipelineDefault), captionThinkingField.Key)
	manualOCRThinkKey     = types.SlotKey(string(types.ImagePipelineDefault), ocrThinkingField.Key)
	observeCaptionKey     = types.SlotKey(string(types.ImagePipelineSmartOCR), keyObservationCaptionEnabled)
)

// TestImagePipelineActionSequences pins how the private switches of each
// pipeline map onto the actions that run. The sequence is the per-image answer to
// "what did this image go through", so it is what a reader of a trace replays
// when an image came out wrong.
//
// The two pipelines own different keys on purpose: the default pipeline asks
// whether to run a step, smartocr asks whether OCR may be spent at all. Both
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
			"observation, caption and OCR", true, true, true, types.ImagePipelineSmartOCR,
			// Preprocess sits between the OCR decision and OCR itself: when the
			// policy says OCR runs, the page is straightened first.
			[]types.ImageActionID{
				types.ImageActionObservation, types.ImageActionPreprocess, types.ImageActionOCR,
			},
			2,
		},
		{
			"observation and caption", true, false, true, types.ImagePipelineSmartOCR,
			// Preprocess sits between the OCR decision and OCR itself: when the
			// policy says OCR runs, the page is straightened first.
			[]types.ImageActionID{
				types.ImageActionObservation, types.ImageActionPreprocess, types.ImageActionOCR,
			},
			2,
		},
		{
			"observation, caption kept", true, true, false, types.ImagePipelineSmartOCR,
			[]types.ImageActionID{types.ImageActionObservation},
			1,
		},
		{
			"observation, caption dropped", true, false, false, types.ImagePipelineSmartOCR,
			[]types.ImageActionID{types.ImageActionObservation},
			1,
		},
		{
			"caption and OCR", false, true, true, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR},
			2,
		},
		{
			// Both actions always run — each reads its own gate — so the
			// sequence is the pipeline's action list even when one is switched
			// off. Which one was off is in the trace (its result says
			// skipped_disabled), and no model call is spent on it.
			"caption only", false, true, false, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR},
			1,
		},
		{
			"OCR only", false, false, true, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR},
			1,
		},
		{
			"nothing at all", false, false, false, types.ImagePipelineDefault,
			[]types.ImageActionID{types.ImageActionCaption, types.ImageActionOCR},
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{
				observeCaptionKey: tc.caption,
				manualCaptionKey:  tc.caption,
				manualOCRKey:      tc.ocr,
			}
			if !tc.attrs {
				// Each pipeline reads its own slot and nothing else, so a row
				// sets the keys its pipeline declares and the other pipeline's
				// keys cannot leak in.
				params = map[string]any{
					manualCaptionKey: tc.caption,
					manualOCRKey:     tc.ocr,
				}
			}
			var r *runContext
			var model *stubVLM
			if tc.attrs && !tc.ocr {
				// A row that does not want OCR to be spent: the observing pipeline
				// has no ceiling switch any more, so the policy is what says no.
				r, model = runImagePipeline(t, tc.attrs, params, policyDecliningOCR())
			} else {
				r, model = runOneImagePipeline(t, tc.attrs, params)
			}
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

// actionDeclaredKeys is the set of keys the shared actions declare, built from
// the declarations rather than listed by hand: the exemption in
// TestPipelineFieldsArePrivate means "the action owns this spelling", and a
// hand-written list would go stale the moment an action gained a tunable.
func actionDeclaredKeys() map[string]bool {
	keys := make(map[string]bool)
	for _, spec := range types.SharedImageActions {
		for _, field := range spec.Fields {
			keys[field.Key] = true
		}
	}
	return keys
}

// TestPipelineFieldsArePrivate holds a pipeline's own field list to two rules:
// one control per key (a duplicate would render two controls for one stored
// value), and one spelling per idea across pipelines for the keys a pipeline
// invents itself. Stored values cannot collide any more — a setting lives under
// the slot of the pipeline that declares it (types.SlotKey), and
// TestParamReadsOnlyItsOwnNamespace pins that isolation — so what is left to
// guard here is the panel and the stored config agreeing on what a word means.
// ACTION-level keys ("<action>.<name>" — the thinking switches) are exempt on
// purpose: the action owns that spelling, so every pipeline that schedules it
// offers the same key.
func TestPipelineFieldsArePrivate(t *testing.T) {
	seen := make(map[string]types.ImagePipelineID)
	actionKeys := actionDeclaredKeys()
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
			// A note carries no value — it is words the pipeline wants shown —
			// so there is nothing for it to default to. Every field that does
			// carry a value must declare what it reads when unset, or the panel
			// would render a control with no state.
			if field.Default == nil && field.Type != types.ImageFieldTypeNote {
				t.Errorf("field %q of pipeline %q has no default, so the panel would show an unset toggle",
					field.Key, spec.ID)
			}
			if field.Type != types.ImageFieldTypeBool &&
				field.Type != types.ImageFieldTypeString &&
				field.Type != types.ImageFieldTypeEnum &&
				field.Type != types.ImageFieldTypeNote {
				t.Errorf("field %q of pipeline %q has an unknown type %q", field.Key, spec.ID, field.Type)
			}
			if other, dup := seen[field.Key]; dup && other != spec.ID {
				if actionKeys[field.Key] {
					continue // an action's own tunable, offered by every pipeline that schedules it
				}
				t.Errorf("field %q is declared by both %q and %q; a key a pipeline invents "+
					"for itself must mean one thing", field.Key, other, spec.ID)
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
	r := &runContext{
		out: types.JSONMap{}, imageInfo: &types.ImageInfo{},
		pipelineID: string(types.ImagePipelineDefault), declared: pipeline.Fields(),
	}
	if got := r.BoolParam(captionFieldKeyOCREnabled); got != true {
		t.Errorf("BoolParam(%q) with no params = %v, want true (the declared default)",
			captionFieldKeyOCREnabled, got)
	}
	// A key this pipeline never declared reads nil rather than panicking: an old
	// stored config may carry parameters for a pipeline that has since changed.
	if got := r.Param("no_such_field"); got != nil {
		t.Errorf("Param(\"no_such_field\") = %v, want nil", got)
	}
}

// TestObservationPipelineDeclaresNoActionControls pins the panel contract of
// smartocr: the observed attributes decide which work happens, so the panel
// gets no switch that turns an action on or off. The two thinking switches are
// the exception — they change how a call runs, not whether one does.
func TestObservationPipelineDeclaresNoActionControls(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineSmartOCR]
	for _, field := range pipeline.Fields() {
		if field.DecidesAction {
			t.Errorf("smartocr declares field %q as an action control; "+
				"the observed attributes decide that, not the user",
				field.Key)
		}
	}
	r := &runContext{
		out: types.JSONMap{}, imageInfo: &types.ImageInfo{},
		pipelineID: string(types.ImagePipelineSmartOCR), declared: pipeline.Fields(),
	}
	if !r.BoolParamOr(smartFieldKeyOCROnUnobserved, true) {
		t.Error("the fallback clause with no params must default to true")
	}
	r.params = map[string]any{
		types.SlotKey(string(types.ImagePipelineSmartOCR), smartFieldKeyOCROnUnobserved): false,
	}
	if r.BoolParamOr(smartFieldKeyOCROnUnobserved, true) {
		t.Error("an explicit clause=false must override the pinned default")
	}
}

// TestPipelineRulesMatchTheirFields pins that a rule names fields the pipeline
// actually offers, and that it names action controls rather than switches that
// only tune a call: a rule over a field that runs anyway would never be broken,
// and a rule naming a field the panel does not render could not be fixed by
// the user who has to fix it.
func TestPipelineRulesMatchTheirFields(t *testing.T) {
	for _, id := range []types.ImagePipelineID{types.ImagePipelineDefault, types.ImagePipelineSmartOCR} {
		pipeline := imagePipelineRegistry[id]
		for _, rule := range pipeline.Rules() {
			if rule.MessageKey == "" {
				t.Errorf("%s rule over %v carries no message key, so the panel could only say that something is wrong",
					id, rule.AtLeastOne)
			}
			for _, key := range rule.AtLeastOne {
				var field types.ImageFieldDef
				found := false
				for _, candidate := range pipeline.Fields() {
					if candidate.Key == key {
						field, found = candidate, true
						break
					}
				}
				if !found {
					t.Errorf("%s rule names field %q, which this pipeline does not declare", id, key)
					continue
				}
				if field.DecidesAction {
					continue
				}
				t.Errorf("%s rule names field %q, which only tunes a call that runs either way", id, key)
			}
		}
	}
}

// TestManualPipelineDeclaresRules pins the other half of the same contract: the
// pipeline that leaves every action to the operator's switches must say so, or
// the panel would never warn that a knowledge base is set to process images by
// running nothing. The two halves together are what keep the "at least one
// action has to stay on" check off the smart pipeline, whose actions it cannot
// switch off, and on the manual one, where they are the user's to decide.
func TestManualPipelineDeclaresRules(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineDefault]
	if len(pipeline.Rules()) == 0 {
		t.Fatal("the manual pipeline declares no rule, so a knowledge base " +
			"set to run nothing would save without warning")
	}
	// A thinking switch tunes the call; like the smart pipeline's, it may be off
	// for every image without leaving anything unprocessed.
	for _, field := range pipeline.Fields() {
		if strings.Contains(field.Key, "thinking") && field.DecidesAction {
			t.Errorf("thinking switch %q must not decide an action; the call runs either way", field.Key)
		}
	}
}

// TestSmartPipelineDeclaresNoRules pins that the pipeline which schedules its
// own actions declares no rule at all — the observed attributes decide the work,
// so no combination of the switches it offers can be invalid, and the panel has
// nothing to warn about.
func TestSmartPipelineDeclaresNoRules(t *testing.T) {
	pipeline := imagePipelineRegistry[types.ImagePipelineSmartOCR]
	if len(pipeline.Rules()) != 0 {
		t.Errorf("smartocr declares %d rules; it switches its own actions on and off", len(pipeline.Rules()))
	}
	if err := pipeline.Validate(map[string]any{
		captionThinkingField.Key: false,
		ocrThinkingField.Key:     false,
	}); err != nil {
		t.Errorf("smartocr with both thinking switches off = %v, want nil", err)
	}
}

// TestUnknownPipelineFieldIsIgnored keeps a stale stored parameter harmless. A
// knowledge base may carry a key the running pipeline no longer declares; it
// must not leak into the trace snapshot nor change the run.
func TestUnknownPipelineFieldIsIgnored(t *testing.T) {
	r, model := runOneImagePipeline(t, false, map[string]any{
		manualCaptionKey:             true,
		manualOCRKey:                 true,
		"removed_by_a_later_release": true,
	})
	if len(model.prompts) != 2 {
		t.Fatalf("VLM calls = %d (%v), want 2", len(model.prompts), model.prompts)
	}
	if _, leaked := r.out[keyConfig].(types.JSONMap)["removed_by_a_later_release"]; leaked {
		t.Error("an undeclared key must not reach the config echo")
	}
}

// TestObservationCaptionSharesOneCall is the point of the whole decomposition:
// with attributes on, describing and observing is one action and therefore one
// model request, and the standalone caption action is never scheduled.
func TestObservationCaptionSharesOneCall(t *testing.T) {
	r, model := runImagePipeline(t, true, map[string]any{
		observeCaptionKey: true,
	}, policyDecliningOCR())
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
}

// TestObservationPipelineTrace pins what a trace row says when OCR is skipped by
// the attribute policy, which is the case image_info alone cannot explain.
func TestObservationPipelineTrace(t *testing.T) {
	r, _ := runImagePipeline(t, true, map[string]any{
		observeCaptionKey: true,
	}, policyDecliningOCR())
	if got := r.out[keyOCRResult]; got != "skipped_attr_policy" {
		t.Errorf(`out[keyOCRResult] = %v, want "skipped_attr_policy"`, got)
	}
	if _, ok := r.out["ocr_text"]; ok {
		t.Error("no OCR action ran, so no ocr slot may exist")
	}
}

// TestDefaultPipelineTrace pins the plain path: both actions run and no policy
// field is invented, because there is nothing to decide from.
func TestDefaultPipelineTrace(t *testing.T) {
	r, _ := runOneImagePipeline(t, false, map[string]any{
		manualCaptionKey: true,
		manualOCRKey:     true,
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
	if got := r.out[keyOCRResult]; got != "done" {
		t.Errorf("plain path: ocr_status = %v, want done (OCR ran and read text)", got)
	}
}

// TestImagePipelineIDsAreStable pins the two ids and the strings that reach the
// trace — the labels a reader greps for.
func TestImagePipelineIDsAreStable(t *testing.T) {
	if got := types.ImagePipelineIDFor(true); got != types.ImagePipelineSmartOCR {
		t.Errorf("ImagePipelineIDFor(true) = %q", got)
	}
	if got := string(types.ImagePipelineSmartOCR); got != "smartocr" {
		t.Errorf(`pipeline id = %q, want "smartocr"`, got)
	}
	if got := string(types.ImagePipelineDefault); got != "default" {
		t.Errorf(`pipeline id = %q, want "default"`, got)
	}
	if got := string(types.NormalizeImagePipelineID(types.ImagePipelineLegacyCaptionOCR)); got != "default" {
		t.Errorf(`legacy id normalized = %q, want "default"`, got)
	}
	if got := string(types.NormalizeImagePipelineID(types.ImagePipelineLegacyObCapOCR)); got != "smartocr" {
		t.Errorf(`legacy id normalized = %q, want "smartocr"`, got)
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

// TestExecuteFailsOnUnimplementedAction keeps the registry's failure mode honest:
// an action this build does not implement is a programming error, so it fails the
// image instead of quietly doing one step less than the pipeline asked for.
func TestExecuteFailsOnUnimplementedAction(t *testing.T) {
	r := &runContext{out: types.JSONMap{}, imageInfo: &types.ImageInfo{}, pipelineID: "default"}
	err := r.execute(context.Background(), types.ImageActionID("not_registered"))
	if err == nil {
		t.Fatal("execute accepted an action this build does not implement")
	}
	if !strings.Contains(err.Error(), "not_registered") {
		t.Errorf("the error must name the action, got %v", err)
	}
	if _, wrote := r.out["action_unimplemented"]; wrote {
		t.Error("a failing execute must not leave a trace key behind")
	}
}

// policyDecliningOCR answers every observed attribute with "not this one", so the
// policy declines OCR and the tests that are not about the OCR decision get a run
// with exactly the calls they are pinning.
func policyDecliningOCR() types.ImageActionsConfig {
	return types.ImageActionsConfig{OCR: types.ImageOCRAction{
		On: []types.ImageAttrCondition{{Prop: "contain.text", Is: "none"}},
	}}
}

// TestParamReadsOnlyItsOwnNamespace pins the isolation Param is built on: the
// switches a pipeline reads are the ones stored under its own slot, another
// pipeline's slot says nothing to it, and the declared default answers when the
// slot is empty. A key stored without a slot is not a second home — nothing
// writes one, and reading it would let a stored value mean two different things
// depending on which pipeline happened to run.
func TestParamReadsOnlyItsOwnNamespace(t *testing.T) {
	r := &runContext{
		out:        types.JSONMap{},
		imageInfo:  &types.ImageInfo{},
		declared:   imagePipelineRegistry[types.ImagePipelineSmartOCR].Fields(),
		pipelineID: string(types.ImagePipelineSmartOCR),
		params: map[string]any{
			types.SlotKey(string(types.ImagePipelineSmartOCR), smartFieldKeyOCROnUnobserved): false,
			"ocr_on_unobserved": true,
		},
	}
	if got := r.BoolParamOr(smartFieldKeyOCROnUnobserved, true); got != false {
		t.Errorf("the pipeline's own slot was not read: got %v, want false", got)
	}
	r.pipelineID = string(types.ImagePipelineDefault)
	if got := r.BoolParamOr(smartFieldKeyOCROnUnobserved, true); got != true {
		t.Errorf("another pipeline read smartocr's slot: got %v, want true (declared default)", got)
	}
}
