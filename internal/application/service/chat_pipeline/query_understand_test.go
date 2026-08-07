package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyIntentPromptOverride_AgentOverrideWins(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": "agent prompt"},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}
	global := map[string]string{"chitchat": "global prompt"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "agent prompt" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "agent prompt")
	}
}

func TestApplyIntentPromptOverride_PreservesAgentWhitespace(t *testing.T) {
	// Agent-supplied prompts with surrounding whitespace must reach the model
	// verbatim; trim is only used for emptiness detection.
	raw := "  agent prompt with trailing newline\n"
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": raw},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}

	if !applyIntentPromptOverride(cm, nil) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != raw {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, raw)
	}
}

func TestApplyIntentPromptOverride_BlankAgentFallsBackToGlobal(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": "   \n\t  "},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}
	global := map[string]string{"chitchat": "global prompt"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "global prompt" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "global prompt")
	}
}

func TestApplyIntentPromptOverride_NoOverrideAndNoGlobal(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}

	if applyIntentPromptOverride(cm, nil) {
		t.Fatal("expected applied=false")
	}
	if cm.SystemPromptOverride != "" {
		t.Errorf("override should remain empty, got %q", cm.SystemPromptOverride)
	}
}

func TestApplyIntentPromptOverride_GlobalOnly(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Intent: types.IntentGreeting},
	}
	global := map[string]string{"greeting": "hi there"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "hi there" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "hi there")
	}
}

// TestParseOutput_UnparsableFallsBackToOriginalQuery pins the degradation
// contract for query understanding: when the LLM returns output that cannot be
// parsed as the structured {"rewrite_query","intent",...} JSON, RewriteQuery
// must remain the original user query (which OnEvent sets before calling
// parseOutput) instead of leaking the raw model text into the downstream
// retrieval query.
func TestParseOutput_UnparsableFallsBackToOriginalQuery(t *testing.T) {
	p := &PluginQueryUnderstand{}
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			RewriteQuery: "original user query",
			Intent:       types.IntentKBSearch,
		},
	}

	p.parseOutput(cm, "The answer is: check the admin console")

	if cm.RewriteQuery != "original user query" {
		t.Fatalf("RewriteQuery = %q, want original user query", cm.RewriteQuery)
	}
	if cm.Intent != types.IntentKBSearch {
		t.Errorf("Intent = %q, want kb_search", cm.Intent)
	}
}

// TestParseOutput_UnparsableBlankDoesNotRewrite verifies that empty LLM output
// also leaves the original query untouched.
func TestParseOutput_UnparsableBlankDoesNotRewrite(t *testing.T) {
	p := &PluginQueryUnderstand{}
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			RewriteQuery: "original user query",
			Intent:       types.IntentKBSearch,
		},
	}

	p.parseOutput(cm, "   \n\t  ")

	if cm.RewriteQuery != "original user query" {
		t.Fatalf("RewriteQuery = %q, want original user query", cm.RewriteQuery)
	}
}

// TestParseOutput_ValidJSONStillAppliesRewrite guards the happy path: a
// well-formed structured output still overrides RewriteQuery and Intent.
func TestParseOutput_ValidJSONStillAppliesRewrite(t *testing.T) {
	p := &PluginQueryUnderstand{}
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			RewriteQuery: "original user query",
			Intent:       types.IntentKBSearch,
		},
	}

	p.parseOutput(cm, `{"rewrite_query":"rewritten query","intent":"summarize"}`)

	if cm.RewriteQuery != "rewritten query" {
		t.Fatalf("RewriteQuery = %q, want rewritten query", cm.RewriteQuery)
	}
	if cm.Intent != types.IntentSummarize {
		t.Errorf("Intent = %q, want summarize", cm.Intent)
	}
}
