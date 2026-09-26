package im

import (
	"context"
	"strings"
	"testing"
)

func TestHelpListsSaveAndShowsUsage(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(newHelpCommand(reg))
	reg.Register(newSaveCommand(&saveKnowledgeStub{}))
	reg.Register(newStopCommand())

	help := newHelpCommand(reg)

	list, err := help.Execute(context.Background(), &CommandContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "`/save`") {
		t.Fatalf("help list missing /save: %q", list.Content)
	}
	if !strings.Contains(list.Content, "将网页保存到当前 IM 渠道配置的知识库") {
		t.Fatalf("help list missing save description: %q", list.Content)
	}

	detail, err := help.Execute(context.Background(), &CommandContext{}, []string{"save"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.Content, "**/save**") {
		t.Fatalf("help save missing title: %q", detail.Content)
	}
	if !strings.Contains(detail.Content, "用法：/save <网页链接>") {
		t.Fatalf("help save missing usage: %q", detail.Content)
	}
}
