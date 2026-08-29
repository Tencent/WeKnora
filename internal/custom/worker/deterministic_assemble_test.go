package worker

import (
	"strings"
	"testing"
)

func TestComposeTranscriptPageKeepsFoundationOrderAndMetadata(t *testing.T) {
	content := composeTranscriptPage("视频一", "video-1", "generation-1", "大纲", "总结", "知识")
	for _, expected := range []string{
		"type: transcript_page",
		"source_video_id: video-1",
		"transcript_generation: generation-1",
		"## 内容大纲",
		"## 智能总结",
		"## 相关知识",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("assembled content does not contain %q: %s", expected, content)
		}
	}
	if strings.Index(content, "大纲") > strings.Index(content, "总结") {
		t.Fatalf("foundation order is incorrect: %s", content)
	}
}

func TestComposeTranscriptPageStripsNestedFrontmatter(t *testing.T) {
	content := composeTranscriptPage("视频一", "video-1", "generation-1", "---\ntype: outline\n---\n\n大纲", "总结", "关联知识暂未就绪")
	if strings.Contains(content, "type: outline") {
		t.Fatalf("nested frontmatter should be stripped: %s", content)
	}
	if !strings.Contains(content, "关联知识暂未就绪") {
		t.Fatalf("knowledge placeholder is missing: %s", content)
	}
}
