package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestPipelineFieldKeysAreOwned enforces the parameter naming rule end to end:
// a key a pipeline declares is either its OWN bare name, or exactly an
// action-declared key it is carrying by reference. A pipeline that invents a
// dotted key of its own would be claiming another owner's namespace, and the
// panel, the stored config and the trace would then disagree about who owns the
// value — which is the failure this rule exists to make impossible.
func TestPipelineFieldKeysAreOwned(t *testing.T) {
	actionKeys := make(map[string]types.ImageActionID)
	for _, spec := range types.SharedImageActions {
		for _, field := range spec.Fields {
			actionKeys[field.Key] = spec.ID
		}
	}
	for id, pipeline := range imagePipelineRegistry {
		for _, field := range pipeline.Fields() {
			if !strings.Contains(field.Key, ".") {
				continue // a bare key belongs to the declaring pipeline
			}
			if _, ok := actionKeys[field.Key]; !ok {
				t.Errorf("pipeline %q declares %q: a dotted key must be an action-declared tunable, "+
					"not a name the pipeline invented", id, field.Key)
			}
		}
	}
}

// TestParamIsIsolatedPerPipeline pins the namespaced lookup: two pipelines may
// declare the same bare key, and neither reads the other's stored value. A key
// stored without a slot is not a second home — nothing writes one, and reading
// it would let a stored value mean two different things depending on which
// pipeline happened to run — so that case is asserted here too: the bare key is
// ignored and the default the field declares is what answers.
func TestParamIsIsolatedPerPipeline(t *testing.T) {
	params := map[string]any{
		"default.thinking":  true,
		"smartocr.thinking": false,
	}
	declared := []types.ImageFieldDef{{Key: "thinking", Default: true}}

	for _, tc := range []struct {
		pipeline string
		want     bool
	}{
		{"default", true},
		{"smartocr", false},
	} {
		r := &runContext{pipelineID: tc.pipeline, params: params, declared: declared}
		if got := r.BoolParam("thinking"); got != tc.want {
			t.Errorf("pipeline %q read thinking=%v, want %v", tc.pipeline, got, tc.want)
		}
	}

	// A key stored without a slot is not read, so the answer is the default the
	// field declares — asserted with a default of false on purpose: a default of
	// true would make this pass whether or not the bare key was read, which is
	// the one way this test could stop proving anything.
	noDefault := []types.ImageFieldDef{{Key: "thinking", Default: false}}
	r := &runContext{
		pipelineID: "smartocr",
		params:     map[string]any{"thinking": true},
		declared:   noDefault,
	}
	if r.BoolParam("thinking") {
		t.Error("a key stored without a slot answered; a setting lives in a slot and nowhere else")
	}

	// With neither stored form present, the declared default answers.
	r = &runContext{pipelineID: "smartocr", params: map[string]any{}, declared: declared}
	if !r.BoolParam("thinking") {
		t.Error("an undeclared-value read did not fall back to the field default")
	}
}
