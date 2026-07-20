package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseStructuredQueryOutput_NewSchema(t *testing.T) {
	out, ok := parseStructuredQueryOutput(`{"rewrite_query":"最新法规","response_mode":"answer","retrieval_need":"required","source_requirement":"web","freshness":"current","image_description":""}`)
	if !ok {
		t.Fatal("expected valid structured output")
	}
	if out.ResponseMode != types.ResponseModeAnswer || out.RetrievalNeed != types.RetrievalNeedRequired ||
		out.SourceRequirement != types.SourceRequirementWeb || out.Freshness != types.FreshnessCurrent {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestParseStructuredQueryOutput_LegacyIntentMigration(t *testing.T) {
	out, ok := parseStructuredQueryOutput(`{"rewrite_query":"最新新闻","intent":"web_search","image_description":""}`)
	if !ok {
		t.Fatal("expected legacy output to be accepted")
	}
	if out.ResponseMode != types.ResponseModeAnswer || out.SourceRequirement != types.SourceRequirementWeb ||
		out.Freshness != types.FreshnessCurrent {
		t.Fatalf("unexpected legacy mapping: %+v", out)
	}
}

func TestParseStructuredQueryOutput_RejectsUnknownEnums(t *testing.T) {
	if _, ok := parseStructuredQueryOutput(`{"rewrite_query":"x","response_mode":"made_up","retrieval_need":"required","source_requirement":"auto","freshness":"any"}`); ok {
		t.Fatal("expected unknown response mode to be rejected")
	}
}

func TestParseOutput_RoutingRunsWhenRewriteDisabled(t *testing.T) {
	plugin := &PluginQueryUnderstand{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{Query: "原始问题", EnableRewrite: false},
		PipelineState: types.PipelineState{
			RewriteQuery:  "原始问题",
			Understanding: types.DefaultQueryUnderstanding(),
		},
	}
	plugin.parseOutput(cm, `{"rewrite_query":"改写问题","response_mode":"answer","retrieval_need":"required","source_requirement":"web","freshness":"current","image_description":""}`)
	if cm.RewriteQuery != "原始问题" {
		t.Fatalf("rewrite query = %q, want original query", cm.RewriteQuery)
	}
	if cm.Understanding.SourceRequirement != types.SourceRequirementWeb || cm.Understanding.Freshness != types.FreshnessCurrent {
		t.Fatalf("routing classification was not applied: %+v", cm.Understanding)
	}
}

func TestApplyManagedResponsePrompt_NoPromptAndNoGlobal(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Understanding: types.QueryUnderstanding{ResponseMode: types.ResponseModeChitchat}},
	}

	if applyManagedResponsePrompt(cm, nil) {
		t.Fatal("expected applied=false")
	}
	if cm.ManagedResponsePrompt != "" {
		t.Errorf("managed prompt should remain empty, got %q", cm.ManagedResponsePrompt)
	}
}

func TestApplyManagedResponsePrompt_GlobalOnly(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Understanding: types.QueryUnderstanding{ResponseMode: types.ResponseModeGreeting}},
	}
	global := map[string]string{"greeting": "hi there"}

	if !applyManagedResponsePrompt(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.ManagedResponsePrompt != "hi there" {
		t.Errorf("managed prompt: got %q, want %q", cm.ManagedResponsePrompt, "hi there")
	}
}

func TestApplyManagedResponsePrompt_RoutingOutcomeWins(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			Understanding: types.QueryUnderstanding{ResponseMode: types.ResponseModeAnswer},
			RetrievalPlan: types.RetrievalPlan{
				Mode:       types.RetrievalPlanNone,
				ReasonCode: types.RetrievalReasonKBUnavailable,
			},
		},
	}
	global := map[string]string{
		"answer":                     "generic answer",
		"knowledge_base_unavailable": "select a knowledge base",
	}

	if !applyManagedResponsePrompt(cm, global) {
		t.Fatal("expected routing outcome prompt to be applied")
	}
	if cm.ManagedResponsePrompt != "select a knowledge base" {
		t.Fatalf("managed prompt = %q", cm.ManagedResponsePrompt)
	}
}
