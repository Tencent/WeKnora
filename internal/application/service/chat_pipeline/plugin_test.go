package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// --- IntoChatMessage tests ---

func TestIntoChatMessage_NoKBRetrieval(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "hello world",
		},
		PipelineState: types.PipelineState{
			Intent: types.IntentChitchat,
		},
	}
	plugin := &PluginIntoChatMessage{messageService: nil}
	nextCalled := false
	err := plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next() was not called")
	}
	if cm.UserContent != "hello world" {
		t.Errorf("UserContent: got %q, want %q", cm.UserContent, "hello world")
	}
}

func TestIntoChatMessage_WithMergeResults(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "test query",
			SummaryConfig: types.SummaryConfig{
				ContextTemplate: "Question: {{query}}\n\nReferences:\n{{contexts}}",
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{Content: "chunk A content"},
				{Content: "chunk B content"},
			},
		},
	}
	plugin := &PluginIntoChatMessage{messageService: nil}
	nextCalled := false
	err := plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next() was not called")
	}
	if cm.UserContent == "" {
		t.Fatal("expected UserContent to be populated")
	}
	if !contains(cm.UserContent, "test query") {
		t.Errorf("UserContent should contain query, got: %s", cm.UserContent)
	}
	if !contains(cm.UserContent, "chunk A content") {
		t.Errorf("UserContent should contain chunk A, got: %s", cm.UserContent)
	}
}

func TestIntoChatMessage_ContextIncludesCitationMetadata(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "test query",
			SummaryConfig: types.SummaryConfig{
				ContextTemplate: "{{contexts}}",
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{
					ID:                "chunk-1",
					Content:           "chunk A content",
					KnowledgeBaseID:   "kb-1",
					KnowledgeID:       "knowledge-1",
					KnowledgeTitle:    "Guide",
					KnowledgeFilename: "guide.md",
				},
			},
		},
	}
	plugin := &PluginIntoChatMessage{messageService: nil}

	err := plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError {
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(cm.RenderedContexts, `<context id="1" chunk_id="chunk-1" kb_id="kb-1" doc="Guide">`) {
		t.Fatalf("RenderedContexts should expose citation metadata, got: %s", cm.RenderedContexts)
	}
}

func TestIntoChatMessage_FAQAndDocumentContextsKeepCitationMetadata(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                    "test query",
			FAQPriorityEnabled:       true,
			FAQDirectAnswerThreshold: 0.9,
			SummaryConfig: types.SummaryConfig{
				ContextTemplate: "{{contexts}}",
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{
					ID:              "faq-chunk",
					Content:         "faq content",
					KnowledgeBaseID: "kb-faq",
					KnowledgeTitle:  "FAQ",
					ChunkType:       string(types.ChunkTypeFAQ),
					Score:           0.95,
				},
				{
					ID:                "doc-chunk",
					Content:           "document content",
					KnowledgeBaseID:   "kb-doc",
					KnowledgeFilename: "manual.pdf",
				},
			},
		},
	}
	plugin := &PluginIntoChatMessage{messageService: nil}

	err := plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError {
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(cm.RenderedContexts, `<context id="FAQ-1" chunk_id="faq-chunk" kb_id="kb-faq" doc="FAQ" match="exact">`) {
		t.Fatalf("FAQ context should expose citation metadata, got: %s", cm.RenderedContexts)
	}
	if !contains(cm.RenderedContexts, `<context id="DOC-1" chunk_id="doc-chunk" kb_id="kb-doc" doc="manual.pdf">`) {
		t.Fatalf("document context should expose citation metadata, got: %s", cm.RenderedContexts)
	}
}

func TestIntoChatMessage_ImageDescriptionAppended(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                   "what is this?",
			ChatModelSupportsVision: false,
		},
		PipelineState: types.PipelineState{
			Intent:           types.IntentChitchat,
			ImageDescription: "a cat sitting on a mat",
		},
	}
	plugin := &PluginIntoChatMessage{messageService: nil}
	_ = plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError {
		return nil
	})
	if !contains(cm.UserContent, "a cat sitting on a mat") {
		t.Errorf("UserContent should contain image description, got: %s", cm.UserContent)
	}
}

func TestAnswerContainsKBCitationTag(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		wantHit bool
	}{
		{
			name:    "self closing kb tag",
			answer:  `The answer is grounded. <kb doc="guide.md" chunk_id="chunk-1" />`,
			wantHit: true,
		},
		{
			name:    "no citation",
			answer:  "The answer is grounded.",
			wantHit: false,
		},
		{
			name:    "keyboard html is not citation",
			answer:  "Press <kbd>Enter</kbd> to continue.",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := answerContainsKBCitationTag(tt.answer); got != tt.wantHit {
				t.Fatalf("answerContainsKBCitationTag(%q) = %v, want %v", tt.answer, got, tt.wantHit)
			}
		})
	}
}

// --- PipelineBuilder tests ---

func TestPipelineBuilder_Basic(t *testing.T) {
	pipeline := types.NewPipelineBuilder().
		Add(types.LOAD_HISTORY).
		Add(types.CHAT_COMPLETION_STREAM).
		Build()

	if len(pipeline) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(pipeline))
	}
	if pipeline[0] != types.LOAD_HISTORY {
		t.Errorf("stage 0: got %v, want %v", pipeline[0], types.LOAD_HISTORY)
	}
}

func TestPipelineBuilder_AddIf(t *testing.T) {
	pipeline := types.NewPipelineBuilder().
		Add(types.LOAD_HISTORY).
		AddIf(false, types.QUERY_UNDERSTAND).
		AddIf(true, types.CHAT_COMPLETION_STREAM).
		Build()

	if len(pipeline) != 2 {
		t.Fatalf("expected 2 stages (QUERY_UNDERSTAND skipped), got %d", len(pipeline))
	}
	if pipeline[1] != types.CHAT_COMPLETION_STREAM {
		t.Errorf("stage 1: got %v, want %v", pipeline[1], types.CHAT_COMPLETION_STREAM)
	}
}

func TestPipelineBuilder_Empty(t *testing.T) {
	pipeline := types.NewPipelineBuilder().Build()
	if len(pipeline) != 0 {
		t.Fatalf("expected 0 stages, got %d", len(pipeline))
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsString(s, substr))
}

func containsString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
