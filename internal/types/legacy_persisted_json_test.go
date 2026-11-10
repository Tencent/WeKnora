package types

// Round-trip guards for the JSON columns this refactor touches. Each literal
// below is a blob the pre-catalog code actually wrote, so a failure means an
// existing deployment loses data the first time the row is read and saved.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// legacyModelParametersJSON is a `models.parameters` value written by the old
// code, carrying every field the pre-refactor ModelParameters struct had.
const legacyModelParametersJSON = `{
  "base_url": "https://api.deepseek.com/v1",
  "api_key": "sk-stored",
  "interface_type": "openai",
  "embedding_parameters": {
    "dimension": 1024,
    "truncate_prompt_tokens": 2048,
    "supports_dimension_override": true
  },
  "parameter_size": "7B",
  "provider": "deepseek",
  "extra_config": {
    "thinking_control": "thinking_type",
    "remote_model_name": "deepseek-reasoner",
    "api_version": "2024-10-21",
    "secret_key": "sk",
    "region": "ap-guangzhou",
    "instruction": "rank these",
    "truncate_prompt_tokens": "4096"
  },
  "custom_headers": {"X-Corp-Route": "llm"},
  "supports_vision": true,
  "context_window": 128000,
  "max_output_tokens": 8192,
  "max_concurrency": 4,
  "app_id": "app",
  "app_secret": "encrypted"
}`

// TestLegacyModelParametersRoundTrip proves that reading an old row and
// writing it back preserves every field. The refactor added
// ModelParameters.Spec; had it renamed or dropped anything, the re-marshalled
// blob would differ from the stored one.
func TestLegacyModelParametersRoundTrip(t *testing.T) {
	var params ModelParameters
	if err := json.Unmarshal([]byte(legacyModelParametersJSON), &params); err != nil {
		t.Fatalf("an old parameters blob no longer unmarshals: %v", err)
	}

	// Strict decode: every key the old code wrote must still map to a field.
	dec := json.NewDecoder(strings.NewReader(legacyModelParametersJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ModelParameters{}); err != nil {
		t.Fatalf("an old parameters key has no field on the new struct: %v", err)
	}

	if params.Spec != nil {
		t.Errorf("Spec must default to nil on a row that predates it, got %+v", params.Spec)
	}

	rewritten, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(legacyModelParametersJSON), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rewritten, &after); err != nil {
		t.Fatal(err)
	}
	for key, want := range before {
		got, ok := after[key]
		if !ok {
			t.Errorf("key %q was dropped when the row was rewritten", key)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("key %q changed on rewrite: stored %v, rewritten %v", key, want, got)
		}
	}
	if _, ok := after["spec"]; ok {
		t.Error("spec must be omitted when unset so untouched rows are not rewritten")
	}
}

// TestLegacyCustomAgentConfigThinking pins the legacy boolean for agent rows
// saved before reasoning_effort existed: thinking true, false and absent must
// each behave exactly as they did.
func TestLegacyCustomAgentConfigThinking(t *testing.T) {
	cases := []struct {
		name string
		blob string
		want *bool
	}{
		{"thinking true", `{"thinking": true}`, boolPtr(true)},
		{"thinking false", `{"thinking": false}`, boolPtr(false)},
		// Absent has always been pinned to false by EnsureDefaults.
		{"thinking absent", `{"max_completion_tokens": 4096}`, boolPtr(false)},
		{"thinking null", `{"thinking": null}`, boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &CustomAgent{}
			if err := json.Unmarshal([]byte(tc.blob), &agent.Config); err != nil {
				t.Fatalf("an old agent config no longer unmarshals: %v", err)
			}
			if agent.Config.ReasoningEffort != "" {
				t.Fatalf("an old row must leave reasoning_effort empty, got %q",
					agent.Config.ReasoningEffort)
			}
			agent.EnsureDefaults()
			if agent.Config.Thinking == nil {
				t.Fatal("EnsureDefaults left Thinking nil")
			}
			if *agent.Config.Thinking != *tc.want {
				t.Fatalf("Thinking = %v, old code produced %v",
					*agent.Config.Thinking, *tc.want)
			}
		})
	}
}

// TestLegacyAgentStepRoundTrip covers the persisted agent history. Old steps
// carry reasoning_content but never a signature or provider metadata, and the
// protocol layers only replay a thinking block when both the content and a
// protocol-tagged signature are present.
func TestLegacyAgentStepRoundTrip(t *testing.T) {
	const legacyStep = `{
	  "thought": "I should search the knowledge base",
	  "reasoning_content": "the user asked about pricing",
	  "tool_calls": [],
	  "timestamp": "2025-01-02T03:04:05Z"
	}`
	var step AgentStep
	if err := json.Unmarshal([]byte(legacyStep), &step); err != nil {
		t.Fatalf("an old agent step no longer unmarshals: %v", err)
	}
	if step.ReasoningContent != "the user asked about pricing" {
		t.Fatalf("reasoning_content was lost: %q", step.ReasoningContent)
	}
	if step.ReasoningSignature != "" {
		t.Fatalf("an old step must carry no signature, got %q", step.ReasoningSignature)
	}
	if step.ReasoningMetadata != nil {
		t.Fatalf("an old step must carry no provider metadata, got %v", step.ReasoningMetadata)
	}
	rewritten, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(rewritten, &after); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reasoning_signature", "reasoning_metadata"} {
		if _, ok := after[key]; ok {
			t.Errorf("%q must be omitted on a step that has none", key)
		}
	}
}

// TestLegacySummaryConfigThinking covers the session-level summary config,
// which gained the same reasoning_effort alias.
func TestLegacySummaryConfigThinking(t *testing.T) {
	var cfg SummaryConfig
	if err := json.Unmarshal([]byte(`{"thinking": true, "max_tokens": 2048}`), &cfg); err != nil {
		t.Fatalf("an old summary config no longer unmarshals: %v", err)
	}
	if cfg.Thinking == nil || !*cfg.Thinking {
		t.Fatal("thinking:true was lost")
	}
	if cfg.ReasoningEffort != "" {
		t.Fatalf("an old row must leave reasoning_effort empty, got %q", cfg.ReasoningEffort)
	}
}
