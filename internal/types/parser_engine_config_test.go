package types

import "testing"

func TestParserEngineConfigResolveChatParserEngine(t *testing.T) {
	config := &ParserEngineConfig{ChatParserEngineRules: []ParserEngineRule{
		{FileTypes: []string{"pdf", ".docx"}, Engine: "mineru"},
		{FileTypes: []string{"xlsx"}, Engine: "markitdown"},
	}}
	for input, expected := range map[string]string{
		"PDF": "mineru", ".docx": "mineru", "xlsx": "markitdown", "txt": "",
	} {
		if actual := config.ResolveChatParserEngine(input); actual != expected {
			t.Fatalf("ResolveChatParserEngine(%q) = %q, want %q", input, actual, expected)
		}
	}
	var nilConfig *ParserEngineConfig
	if actual := nilConfig.ResolveChatParserEngine("pdf"); actual != "" {
		t.Fatalf("nil config resolved %q", actual)
	}
}
