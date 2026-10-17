package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// thinkingCall is one model request made by a pipeline, with the thinking
// switch the caller asked for — the value the remote model actually receives.
type thinkingCall struct {
	prompt   string
	thinking bool
}

// thinkingStubVLM records the switch alongside the prompt so a test can pin
// "what did the pipeline ask the model for", not just how many times it asked.
type thinkingStubVLM struct{ calls []thinkingCall }

func (s *thinkingStubVLM) record(prompt string, thinking *bool) string {
	var on bool
	if thinking != nil {
		on = *thinking
	}
	s.calls = append(s.calls, thinkingCall{prompt: prompt, thinking: on})
	if strings.Contains(prompt, "Observe the following") {
		return "contain.text: block\ncontain.data_visual: true\nDESCRIPTION: a stubbed image\n"
	}
	return "stubbed answer\n"
}

func (s *thinkingStubVLM) Predict(_ context.Context, _ [][]byte, prompt string) (string, error) {
	return s.record(prompt, nil), nil
}

func (s *thinkingStubVLM) PredictWithOptions(
	_ context.Context, _ [][]byte, prompt string, opts *vlm.PredictOptions,
) (string, error) {
	var on *bool
	if opts != nil {
		on = opts.Thinking
	}
	return s.record(prompt, on), nil
}

func (s *thinkingStubVLM) GetModelName() string { return "thinking-stub" }
func (s *thinkingStubVLM) GetModelID() string   { return "thinking-stub" }

var _ vlm.VLM = (*thinkingStubVLM)(nil)

// runThinkingPipeline drives one pipeline run against a recording stub. params
// are the pipeline's private tunables; an empty map means "defaults".
func runThinkingPipeline(t *testing.T, attrsEnabled bool, params map[string]any) (*runContext, *thinkingStubVLM) {
	t.Helper()
	model := &thinkingStubVLM{}
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
		payload: payload,
		// The pipeline id is what namespaces every param lookup; leaving it
		// empty would make the run read bare keys nothing writes and fall back
		// to the declared defaults, so a test about a stored switch would be
		// asserting that the default is off.
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

// TestThinkingDefaultsToOff pins the decision that an untouched knowledge base
// runs the cheaper, sturdier call: a quantized reasoning model that thinks its
// way out of a short prompt will often emit nothing but reasoning and hand back
// an empty OCR slot, which is worse than not turning thinking on at all. So
// thinking is off unless somebody asked for it, per action.
func TestThinkingDefaultsToOff(t *testing.T) {
	r, model := runThinkingPipeline(t, false, map[string]any{
		manualCaptionKey: true,
		manualOCRKey:     true,
	})
	if len(model.calls) != 2 {
		t.Fatalf("VLM calls = %d (%v), want 2", len(model.calls), model.calls)
	}
	for _, call := range model.calls {
		if call.thinking {
			t.Errorf("call %q ran with thinking on; the default must be off", call.prompt)
		}
	}
	// The switch is recorded in the trace too, so a reader of a run can tell
	// which mode produced a chunk.
	if got := r.out[captionThinkingField.Key]; got != false {
		t.Errorf(`out[%q] = %v, want false`, captionThinkingField.Key, got)
	}
	if got := r.out[ocrThinkingField.Key]; got != false {
		t.Errorf(`out[%q] = %v, want false`, ocrThinkingField.Key, got)
	}
}

// TestThinkingFollowsTheStoredSwitch is the escape hatch: a knowledge base whose
// images came out empty can ask for the slower, thinking call without touching
// the model, and the switch is honoured for exactly the action that asked.
func TestThinkingFollowsTheStoredSwitch(t *testing.T) {
	r, model := runThinkingPipeline(t, false, map[string]any{
		manualCaptionKey:      true,
		manualOCRKey:          true,
		manualCaptionThinkKey: true,
	})
	if len(model.calls) != 2 {
		t.Fatalf("VLM calls = %d (%v), want 2", len(model.calls), model.calls)
	}
	if !model.calls[0].thinking {
		t.Errorf("caption ran with thinking off; caption_thinking=true did not reach the model")
	}
	if model.calls[1].thinking {
		t.Errorf("OCR ran with thinking on; the switch is per action, and only caption asked")
	}
	if got := r.out[captionThinkingField.Key]; got != true {
		t.Errorf(`out[%q] = %v, want true`, captionThinkingField.Key, got)
	}
	if got := r.out[ocrThinkingField.Key]; got != false {
		t.Errorf(`out[%q] = %v, want false`, ocrThinkingField.Key, got)
	}
}

// TestObservationPipelineThinkingKeys pins that the thinking switches are
// ACTION-level keys this pipeline carries the same way the default one does:
// observation.thinking_enabled / ocr.thinking_enabled drive the calls, the
// pre-unification private keys (describe_thinking / text_thinking) are stale and
// ignored, and the trace records the same action keys.
func TestObservationPipelineThinkingKeys(t *testing.T) {
	r, model := runThinkingPipeline(t, true, map[string]any{
		observeCaptionKey: true,
		// Stale keys from before the action-level unification: present in a
		// stored config, honoured by nothing.
		"describe_thinking": true,
		"text_thinking":     true,
		// The action-level switches this pipeline declares, stored the way the
		// panel stores them: under the slot of the pipeline that offers them.
		types.SlotKey(string(types.ImagePipelineSmartOCR), observationThinkingField.Key): false,
		types.SlotKey(string(types.ImagePipelineSmartOCR), ocrThinkingField.Key):         true,
	})
	if len(model.calls) != 2 {
		t.Fatalf("VLM calls = %d (%v), want 2", len(model.calls), model.calls)
	}
	if model.calls[0].thinking {
		t.Error("the observation call ran with thinking on; the observation switch was ignored")
	}
	if !model.calls[1].thinking {
		t.Error("the OCR call ran with thinking off; the OCR switch was ignored")
	}
	if got := r.out[observationThinkingField.Key]; got != false {
		t.Errorf(`out[%q] = %v, want false`, observationThinkingField.Key, got)
	}
	if got := r.out[ocrThinkingField.Key]; got != true {
		t.Errorf(`out[%q] = %v, want true`, ocrThinkingField.Key, got)
	}
	// The stale private keys must not leak into the trace.
	if _, ok := r.out["describe_thinking"]; ok {
		t.Error("the stale describe_thinking key must not appear in the trace")
	}
	if _, ok := r.out["text_thinking"]; ok {
		t.Error("the stale text_thinking key must not appear in the trace")
	}
}
