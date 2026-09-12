package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// NewWikiPageEvaluationRunner exposes the page-update prompt orchestration to
// the isolated evaluation command. It owns no repositories or task queues.
// The fixed production template, warmup, metadata and request coalescing are
// shared with ordinary Wiki page updates; the supplied model controls budgets.
func NewWikiPageEvaluationRunner(model chat.Chat) func(context.Context, map[string]string) (string, error) {
	s := &wikiIngestService{}
	return func(ctx context.Context, data map[string]string) (string, error) {
		return s.generateWithTemplate(ctx, model, agent.WikiPageModifyUserPrompt, data)
	}
}
