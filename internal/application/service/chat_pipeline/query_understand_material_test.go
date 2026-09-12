package chatpipeline

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestQueryUnderstandingUsesReferencesAndActualImageInputs(t *testing.T) {
	plugin := &PluginQueryUnderstand{config: &config.Config{Conversation: &config.ConversationConfig{
		RewritePromptUser: "{{query}}", RewritePromptSystem: "rewrite",
	}}}
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "这个怎么解决", EnableRewrite: true,
			RewriteContext: "reference ERROR-7421",
			Images:         []string{"data:image/png;base64,test"},
			Attachments: types.MessageAttachments{{
				FileName: "screenshot.png", IsImage: true, ImageIndex: 1, Content: "OCR",
			}},
		},
		PipelineState: types.PipelineState{QuotedContext: "reference ERROR-7421"},
	}
	for _, images := range []int{0, 1} {
		_, prompt := plugin.buildPrompts(t.Context(), manage, nil, images)
		if !strings.Contains(prompt, "这个怎么解决") || !strings.Contains(prompt, "ERROR-7421") {
			t.Fatal("references did not reach query understanding with the current question")
		}
		if (images == 0) != strings.Contains(prompt, "NOT available") ||
			(images == 0) != strings.Contains(prompt, "<no_image_attached />") {
			t.Fatal("image availability did not follow the actual selected model input")
		}
	}
	manage.RewriteContext = ""
	_, prompt := plugin.buildPrompts(t.Context(), manage, nil, 0)
	if strings.Contains(prompt, "ERROR-7421") || !strings.Contains(prompt, "<images_uploaded count=\"1\" />") {
		t.Fatal("legacy IM quotes and upload markers must retain their existing behavior")
	}
	manage.EnableRewrite, manage.Images = false, nil
	called := false
	err := plugin.OnEvent(t.Context(), types.QUERY_UNDERSTAND, manage, func() *PluginError {
		called = true
		return nil
	})
	if err != nil || !called || manage.RewriteQuery != manage.Query {
		t.Fatal("rewrite-disabled text requests must keep their original retrieval query")
	}
}
