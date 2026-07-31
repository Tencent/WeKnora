package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestWikiLintTitleMatcherFindsOverlappingTitlesOnce(t *testing.T) {
	matcher := newWikiLintTitleMatcher(map[string]string{
		"entity/openai": "OpenAI",
		"concept/ai":    "AI",
		"entity/rag":    "RAG",
	})

	matches := matcher.Find("OpenAI uses RAG. OPENAI appears again.")
	got := make(map[string]bool, len(matches))
	for _, match := range matches {
		got[match.Slug] = true
	}
	assert.Equal(t, map[string]bool{
		"entity/openai": true,
		"entity/rag":    true,
	}, got)
}

func TestWikiLintTitleMatcherHonorsASCIIBoundaries(t *testing.T) {
	matcher := newWikiLintTitleMatcher(map[string]string{"concept/ai": "AI"})
	assert.Empty(t, matcher.Find("OpenAI"))
	assert.Len(t, matcher.Find("AI-powered and AI技术"), 1)
}

func TestWikiLintTitleMatcherEmptyInput(t *testing.T) {
	assert.Empty(t, newWikiLintTitleMatcher(nil).Find("anything"))
}

func TestWikiIssueVerifierUsesTypedOrphanAndEmptyPostconditions(t *testing.T) {
	service := &wikiPageService{}
	attempt := &types.WikiRepairAttempt{BeforeVersion: 1}

	orphan := &types.WikiPageIssue{IssueType: string(LintIssueOrphanPage)}
	page := &types.WikiPage{Version: 1}
	assert.Error(t, service.verifyWikiIssueResolution(context.Background(), orphan, page, attempt))
	page.InLinks = types.StringArray{"concept/source"}
	assert.NoError(t, service.verifyWikiIssueResolution(context.Background(), orphan, page, attempt))

	empty := &types.WikiPageIssue{IssueType: string(LintIssueEmptyContent)}
	page.Content = "too short"
	assert.Error(t, service.verifyWikiIssueResolution(context.Background(), empty, page, attempt))
	page.Content = "这是一段超过五十个字符的内容，用来确认空页面规则按照字符而不是 UTF-8 字节数进行验证。" +
		"修复结果必须真正达到规则阈值之后才能关闭问题。"
	assert.NoError(t, service.verifyWikiIssueResolution(context.Background(), empty, page, attempt))
}
