package types

import "testing"

func TestCustomAgentConfigResolveChatParserEngine(t *testing.T) {
	config := &CustomAgentConfig{ChatParserEngineRules: []ParserEngineRule{
		{FileTypes: []string{"pdf", ".pptx"}, Engine: "mineru"},
		{FileTypes: []string{"png", "jpg"}, Engine: "paddleocr_vl"},
	}}
	for input, expected := range map[string]string{
		"PDF": "mineru", ".pptx": "mineru", "png": "paddleocr_vl", "txt": "",
	} {
		if actual := config.ResolveChatParserEngine(input); actual != expected {
			t.Fatalf("ResolveChatParserEngine(%q) = %q, want %q", input, actual, expected)
		}
	}
	var nilConfig *CustomAgentConfig
	if actual := nilConfig.ResolveChatParserEngine("pdf"); actual != "" {
		t.Fatalf("nil config resolved %q", actual)
	}
}

func TestEnsureDefaults_ThinkingExplicitFalse(t *testing.T) {
	agent := &CustomAgent{Config: CustomAgentConfig{}}
	agent.EnsureDefaults()
	if agent.Config.Thinking == nil {
		t.Fatal("EnsureDefaults should set Thinking to explicit false when unset")
	}
	if *agent.Config.Thinking {
		t.Fatal("default Thinking should be false")
	}
}

func TestEnsureDefaults_ThinkingPreservesTrue(t *testing.T) {
	enabled := true
	agent := &CustomAgent{Config: CustomAgentConfig{Thinking: &enabled}}
	agent.EnsureDefaults()
	if agent.Config.Thinking == nil || !*agent.Config.Thinking {
		t.Fatal("EnsureDefaults must not overwrite an explicit Thinking=true")
	}
}

func TestEnsureDefaults_MaxCompletionTokensByMode(t *testing.T) {
	qa := &CustomAgent{Config: CustomAgentConfig{AgentMode: AgentModeQuickAnswer}}
	qa.EnsureDefaults()
	if qa.Config.MaxCompletionTokens != 0 {
		t.Fatalf("unset max_completion_tokens must stay 0 (follow default at call time), got %d",
			qa.Config.MaxCompletionTokens)
	}

	sr := &CustomAgent{Config: CustomAgentConfig{AgentMode: AgentModeSmartReasoning}}
	sr.EnsureDefaults()
	if sr.Config.MaxCompletionTokens != 0 {
		t.Fatalf("unset smart-reasoning max_completion_tokens must stay 0, got %d",
			sr.Config.MaxCompletionTokens)
	}

	explicit := &CustomAgent{Config: CustomAgentConfig{
		AgentMode:           AgentModeSmartReasoning,
		MaxCompletionTokens: 64000,
	}}
	explicit.EnsureDefaults()
	if explicit.Config.MaxCompletionTokens != 64000 {
		t.Fatalf("EnsureDefaults must preserve explicit MaxCompletionTokens, got %d",
			explicit.Config.MaxCompletionTokens)
	}
}

func TestEnsureDefaults_CitationsDefaultEnabledAndPreserveFalse(t *testing.T) {
	legacy := &CustomAgent{Config: CustomAgentConfig{}}
	legacy.EnsureDefaults()
	if legacy.Config.CitationEnabled == nil || !*legacy.Config.CitationEnabled {
		t.Fatal("legacy agents must default citation output to enabled")
	}

	disabled := false
	explicit := &CustomAgent{Config: CustomAgentConfig{CitationEnabled: &disabled}}
	explicit.EnsureDefaults()
	if explicit.Config.CitationEnabled == nil || *explicit.Config.CitationEnabled {
		t.Fatal("EnsureDefaults must preserve explicit citation_enabled=false")
	}
}
