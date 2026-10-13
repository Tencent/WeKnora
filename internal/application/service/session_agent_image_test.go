package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
)

// The QA path type-asserts the engine. This fails the build if that method moves.
var _ dataURIImageDescriber = (*agent.AgentEngine)(nil)

func TestComposeAgentImageQueryPassesImagesToAVisionModel(t *testing.T) {
	query, images := composeAgentImageQuery(context.Background(), "看这张图", "", true,
		[]string{"data:image/png;base64,AAAA"},
		func(context.Context, []string) []string {
			t.Fatal("vision models should receive the image, not a description")
			return nil
		})
	if query != "看这张图" || len(images) != 1 {
		t.Fatalf("query=%q images=%v", query, images)
	}
}

func TestComposeAgentImageQueryUsesVLMWhenTheChatModelCannotSee(t *testing.T) {
	var got []string
	query, images := composeAgentImageQuery(context.Background(), "看这张图", "", false,
		[]string{"data:image/png;base64,AAAA"},
		func(_ context.Context, urls []string) []string {
			got = urls
			return []string{"  一张收据  ", ""}
		})
	if len(images) != 0 {
		t.Fatalf("text model still received images: %v", images)
	}
	if !strings.Contains(query, "一张收据") || !strings.Contains(query, "[用户上传图片内容]") {
		t.Fatalf("query = %q", query)
	}
	if len(got) != 1 {
		t.Fatalf("describer urls = %v", got)
	}
}

func TestComposeAgentImageQueryKeepsAnExistingDescription(t *testing.T) {
	query, images := composeAgentImageQuery(context.Background(), "看这张图", "已有说明", false,
		[]string{"data:image/png;base64,AAAA"},
		func(context.Context, []string) []string {
			t.Fatal("an existing description should not call the VLM again")
			return nil
		})
	if len(images) != 0 || !strings.Contains(query, "已有说明") {
		t.Fatalf("query=%q images=%v", query, images)
	}
}
