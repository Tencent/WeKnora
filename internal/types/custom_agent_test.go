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
