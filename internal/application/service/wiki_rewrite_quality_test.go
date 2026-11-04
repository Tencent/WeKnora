package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestWikiRewriteQuality(t *testing.T) {
	line := strings.Repeat("中文内容", 20)
	unique := make([]rune, 17000)
	for i := range unique {
		unique[i] = rune(0x4e00 + i)
	}
	tests := []struct {
		name, content string
		data          map[string]string
		bad           bool
	}{
		{"unpunctuated loop", strings.Repeat("重复的词组不断生成", 500), nil, true},
		{"sentence loop", strings.Repeat(line+"。", 10), nil, true},
		{"short line loop", strings.Repeat("然而，冥皇出手干预，导致局势逆转。\n\n", 100), nil, true},
		{"loop", strings.Repeat(line+"\n\n", 10), nil, true},
		{"growth", string(unique), map[string]string{"ExistingContent": "短文"}, true},
		{"grounded long page", string(unique), map[string]string{"NewContent": string(unique[:6000])}, false},
		{"short refrain", strings.Repeat("相同小标题\n", 10), nil, false},
		{"code", "```\n" + strings.Repeat(line+"\n", 5) + "```", nil, false},
		{"table", strings.Repeat("| "+line+" |\n", 5), nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateWikiRewrite(tt.content, tt.data); (err != nil) != tt.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func TestWikiContinuationOverlap(t *testing.T) {
	overlap := strings.Repeat("边界内容", 20)
	if got := trimWikiContinuationOverlap("前文"+overlap, overlap+"新增事实"); got != "新增事实" {
		t.Fatal(got)
	}
	if got := trimWikiContinuationOverlap("短语", "短语继续"); got != "短语继续" {
		t.Fatal(got)
	}
	if got := trimWikiContinuationOverlap(overlap+"不同结尾", overlap+"新增事实"); got != overlap+"新增事实" {
		t.Fatal(got)
	}
}
func TestWikiRewriteRejectsLoopBeforeContinuation(t *testing.T) {
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{{Content: strings.Repeat(strings.Repeat("事实", 40)+"\n\n", 20), FinishReason: "length"}}}
	service := &wikiIngestService{}
	_, err := service.generateWithTemplateResult(context.Background(), model, agent.WikiPageModifyUserPrompt, modifyTemplateData())
	if err == nil || len(model.calls) != 1 {
		t.Fatalf("error=%v calls=%d", err, len(model.calls))
	}
}
