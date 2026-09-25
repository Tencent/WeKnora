package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// scriptedTemplateChatModel replays a fixed sequence of responses, one per
// Chat call, and records every request it was handed.
type scriptedTemplateChatModel struct {
	responses []*types.ChatResponse
	calls     [][]chat.Message
}

func (m *scriptedTemplateChatModel) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls = append(m.calls, append([]chat.Message(nil), messages...))
	if len(m.responses) == 0 {
		return nil, errors.New("scriptedTemplateChatModel: unexpected extra call")
	}
	next := m.responses[0]
	m.responses = m.responses[1:]
	return next, nil
}

func (m *scriptedTemplateChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *scriptedTemplateChatModel) GetModelName() string { return "scripted" }
func (m *scriptedTemplateChatModel) GetModelID() string   { return "scripted" }

// certLedgerRows renders a Markdown table row.
func certLedgerRow(i int) string {
	return fmt.Sprintf("| %d | 持证人%d | 杭州安恒信息技术股份有限公司 | 2020-01-01 | 2099-01-01 |", i, i)
}

// certLedgerBlock renders the first n rows of a 146-row certificate ledger — the
// shape of the production page that came back short.
func certLedgerBlock(first, last int) string {
	var b strings.Builder
	b.WriteString("SUMMARY: 持证人台账\n# 持证人台账\n\n| 编号 | 姓名 | 所属公司 | 有效期起 | 有效期止 |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for i := first; i <= last; i++ {
		b.WriteString(certLedgerRow(i))
		b.WriteString("\n")
	}
	return b.String()
}

func countLedgerRows(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "|") && strings.Trim(line, "|-: \t") != "" {
			n++
		}
	}
	return n
}

func modifyTemplateData() map[string]string {
	return map[string]string{
		"HasAdditions":         "1",
		"PageSlug":             "entity/cisp-pte",
		"PageTitle":            "CISP-PTE 持证人台账",
		"PageType":             "entity",
		"ExistingContent":      "(New page)",
		"NewContent":           certLedgerBlock(1, 146),
		"AvailableSlugs":       "",
		"Language":             "Chinese",
		"InstructionScope":     "wiki_content",
		"SharedSourceContexts": "",
	}
}

// TestPageRewriteContinuesWhenProviderHitsCompletionBudget is the regression
// for the "long list comes back short" report: the editor emits a page top-down,
// the provider stops it at the completion budget (finish_reason=length) and the
// fragment used to be persisted verbatim — a 146-row source table became a
// 119-row page with nothing in the logs saying "truncated".
func TestPageRewriteContinuesWhenProviderHitsCompletionBudget(t *testing.T) {
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: certLedgerBlock(1, 119), FinishReason: "length"},
		{Content: certLedgerBlock(120, 146), FinishReason: "stop"},
	}}
	service := &wikiIngestService{}

	result, err := service.generateWithTemplateResult(
		context.Background(), model, agent.WikiPageModifyUserPrompt, modifyTemplateData())
	if err != nil {
		t.Fatalf("generateWithTemplateResult() error = %v", err)
	}

	if got := countLedgerRows(result.Content); got != 148 {
		t.Fatalf("stitched page has %d table rows, want 148 (146 holders + header + separator)", got)
	}
	if !strings.Contains(result.Content, certLedgerRow(146)) {
		t.Fatal("stitched page is missing the last holder row")
	}
	if strings.Contains(result.Content, wikiPageModifyContinuationDone) {
		t.Fatal("continuation sentinel leaked into the page")
	}
	if len(model.calls) != 2 {
		t.Fatalf("Chat called %d times, want 2 (initial + one continuation)", len(model.calls))
	}

	// The continuation must replay the partial page as an assistant turn and
	// follow it with the continuation instruction — without that the model
	// answers the original question again from the top (#3446).
	second := model.calls[1]
	if len(second) != 4 {
		t.Fatalf("continuation request has %d messages, want 4 (system, user, assistant, user)", len(second))
	}
	if second[2].Role != "assistant" || second[2].Content != certLedgerBlock(1, 119) {
		t.Fatal("continuation request did not replay the partial page as an assistant turn")
	}
	if second[3].Role != "user" || second[3].Content != agent.WikiPageModifyContinuationPrompt {
		t.Fatalf("continuation request missing the continuation instruction: %q", second[3].Content)
	}
	// The evidence block must survive: the tail is written from the same source
	// material, not from the model's memory of the fragment.
	if !strings.Contains(second[1].Content, certLedgerRow(146)) {
		t.Fatal("continuation request dropped the source evidence block")
	}
}

// TestPageRewriteStopsAtContinuationCap keeps the loop bounded: a provider that
// stops on length every single round must not be retried forever.
func TestPageRewriteStopsAtContinuationCap(t *testing.T) {
	responses := make([]*types.ChatResponse, 0, wikiPageModifyMaxContinuations+1)
	for i := 0; i <= wikiPageModifyMaxContinuations; i++ {
		responses = append(responses, &types.ChatResponse{
			Content:      certLedgerRow(i+1) + "\n",
			FinishReason: "length",
		})
	}
	model := &scriptedTemplateChatModel{responses: responses}
	service := &wikiIngestService{}

	_, err := service.generateWithTemplateResult(
		context.Background(), model, agent.WikiPageModifyUserPrompt, modifyTemplateData())
	if !errors.Is(err, errWikiPageRewriteTruncated) {
		t.Fatalf("error = %v, want errWikiPageRewriteTruncated", err)
	}
	if len(model.calls) != wikiPageModifyMaxContinuations+1 {
		t.Fatalf("Chat called %d times, want %d", len(model.calls), wikiPageModifyMaxContinuations+1)
	}
}

// TestPageRewriteAcceptsCompletePageUnchanged pins the no-op path: a rewrite
// that stops naturally is returned as-is, with a single call.
func TestPageRewriteAcceptsCompletePageUnchanged(t *testing.T) {
	page := certLedgerBlock(1, 146)
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: page, FinishReason: "stop"},
	}}
	service := &wikiIngestService{}

	result, err := service.generateWithTemplateResult(
		context.Background(), model, agent.WikiPageModifyUserPrompt, modifyTemplateData())
	if err != nil {
		t.Fatalf("generateWithTemplateResult() error = %v", err)
	}
	if result.Content != page || result.FinishReason != "stop" {
		t.Fatal("a naturally stopped rewrite must be returned verbatim")
	}
	if len(model.calls) != 1 {
		t.Fatalf("Chat called %d times, want 1", len(model.calls))
	}
}

// TestContinuationIsScopedToPageRewrites keeps the single-call behaviour for the
// JSON-producing prompts: stitching a truncated JSON document is the parse
// path's job, not this loop's.
func TestContinuationIsScopedToPageRewrites(t *testing.T) {
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: `{"entities":[`, FinishReason: "length"},
	}}
	service := &wikiIngestService{}

	result, err := service.generateWithTemplateResult(
		context.Background(), model, `Content={{.Content}}`, map[string]string{"Content": "hello"})
	if err != nil {
		t.Fatalf("generateWithTemplateResult() error = %v", err)
	}
	if result.Content != `{"entities":[` || result.FinishReason != "length" {
		t.Fatalf("non-page prompt was altered: %#v", result)
	}
	if len(model.calls) != 1 {
		t.Fatalf("Chat called %d times, want 1", len(model.calls))
	}
}

// TestPageRewriteContinuationDoneSentinel checks that a model which reports the
// page was already complete does not have its sentinel appended to the page.
func TestPageRewriteContinuationDoneSentinel(t *testing.T) {
	page := certLedgerBlock(1, 146)
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: page, FinishReason: "length"},
		{Content: " (complete) ", FinishReason: "length"},
	}}
	service := &wikiIngestService{}

	result, err := service.generateWithTemplateResult(
		context.Background(), model, agent.WikiPageModifyUserPrompt, modifyTemplateData())
	if err != nil {
		t.Fatalf("generateWithTemplateResult() error = %v", err)
	}
	if result.Content != page {
		t.Fatalf("sentinel was appended to the page: %q", result.Content)
	}
}
