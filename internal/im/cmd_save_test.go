package im

import (
	"context"
	"errors"
	"strings"
	"testing"

	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
)

type saveKnowledgeStub struct {
	lastKBID     string
	lastURL      string
	lastChannel  string
	lastFileName string
	lastFileType string
	err          error
	called       bool
}

func (s *saveKnowledgeStub) CreateKnowledgeFromURL(
	ctx context.Context,
	kbID string,
	url string,
	fileName string,
	fileType string,
	enableMultimodel *bool,
	title string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.called = true
	s.lastKBID = kbID
	s.lastURL = url
	s.lastChannel = channel
	s.lastFileName = fileName
	s.lastFileType = fileType
	if s.err != nil {
		return nil, s.err
	}
	return &types.Knowledge{ID: "k1", KnowledgeBaseID: kbID}, nil
}

func TestSaveCommandValidation(t *testing.T) {
	cmd := newSaveCommand(&saveKnowledgeStub{})

	cases := []struct {
		name string
		args []string
		kbID string
		want string
	}{
		{name: "missing url", args: nil, want: "用法：/save <网页链接>"},
		{name: "blank url", args: []string{"  "}, want: "用法：/save <网页链接>"},
		{name: "bad scheme", args: []string{"ftp://example.com/a"}, want: "链接无效"},
		{name: "no host", args: []string{"https://"}, want: "链接无效"},
		{name: "no kb", args: []string{"https://example.com/a"}, kbID: "", want: "尚未配置保存目标知识库"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := cmd.Execute(context.Background(), &CommandContext{KnowledgeBaseID: tc.kbID}, tc.args)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if res == nil || !strings.Contains(res.Content, tc.want) {
				t.Fatalf("Content = %q, want substring %q", res.Content, tc.want)
			}
		})
	}
}

func TestSaveCommandSuccess(t *testing.T) {
	stub := &saveKnowledgeStub{}
	cmd := newSaveCommand(stub)

	res, err := cmd.Execute(context.Background(), &CommandContext{
		KnowledgeBaseID: "kb-1",
		Platform:        "wechat",
	}, []string{"https://example.com/page"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !stub.called {
		t.Fatal("expected CreateKnowledgeFromURL to be called")
	}
	if stub.lastKBID != "kb-1" || stub.lastURL != "https://example.com/page" {
		t.Fatalf("unexpected call: kb=%q url=%q", stub.lastKBID, stub.lastURL)
	}
	if stub.lastFileName != "" || stub.lastFileType != "" {
		t.Fatalf("expected empty fileName/fileType for webpage crawl, got %q/%q", stub.lastFileName, stub.lastFileType)
	}
	if stub.lastChannel != types.ChannelWechat {
		t.Fatalf("channel = %q, want %q", stub.lastChannel, types.ChannelWechat)
	}
	if !strings.Contains(res.Content, "✅ 已提交到知识库") || !strings.Contains(res.Content, "https://example.com/page") {
		t.Fatalf("unexpected success content: %q", res.Content)
	}
	if strings.Contains(res.Content, "解析完成") || strings.Contains(strings.ToLower(res.Content), "embedding") {
		t.Fatalf("success message must not claim parse/embedding finished: %q", res.Content)
	}
}

func TestSaveCommandInvalidURLFromService(t *testing.T) {
	stub := &saveKnowledgeStub{err: appservice.ErrInvalidURL}
	cmd := newSaveCommand(stub)

	res, err := cmd.Execute(context.Background(), &CommandContext{
		KnowledgeBaseID: "kb-1",
		Platform:        "wechat",
	}, []string{"https://example.com/page"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Content, "链接无效") {
		t.Fatalf("Content = %q, want friendly invalid-url tip", res.Content)
	}
}

func TestSaveCommandInfraError(t *testing.T) {
	stub := &saveKnowledgeStub{err: errors.New("db down")}
	cmd := newSaveCommand(stub)

	_, err := cmd.Execute(context.Background(), &CommandContext{
		KnowledgeBaseID: "kb-1",
	}, []string{"https://example.com/page"})
	if err == nil {
		t.Fatal("expected infrastructure error")
	}
}
