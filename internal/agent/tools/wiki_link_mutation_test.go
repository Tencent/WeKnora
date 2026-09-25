package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type linkMutationWikiService struct {
	interfaces.WikiPageService
	pages       map[string]*types.WikiPage
	failUpdates map[string]bool
	updateCalls int
}

func (s *linkMutationWikiService) GetPageBySlug(_ context.Context, _, slug string) (*types.WikiPage, error) {
	page := s.pages[slug]
	if page == nil {
		return nil, errors.New("not found")
	}
	copyPage := *page
	return &copyPage, nil
}

func (s *linkMutationWikiService) UpdateAutoLinkedContent(_ context.Context, page *types.WikiPage) error {
	s.updateCalls++
	if s.failUpdates[page.Slug] {
		return errors.New("write failed")
	}
	copyPage := *page
	s.pages[page.Slug] = &copyPage
	return nil
}

func TestIncomingWikiRewriteCanBeCompensated(t *testing.T) {
	service := &linkMutationWikiService{
		pages: map[string]*types.WikiPage{
			"source/a": {Slug: "source/a", Content: "see [[concept/old]]"},
			"source/b": {Slug: "source/b", Content: "also [[concept/old]]"},
		},
		failUpdates: map[string]bool{"source/b": true},
	}
	changes, updated, err := applyIncomingWikiContentRewrite(
		context.Background(), service, "kb-1", []string{"source/a", "source/b"},
		func(content string) (string, bool) {
			if content == "see [[concept/old]]" {
				return "see [[concept/new]]", true
			}
			if content == "also [[concept/old]]" {
				return "also [[concept/new]]", true
			}
			return content, false
		},
	)
	if err == nil || len(changes) != 1 || len(updated) != 1 {
		t.Fatalf("expected one applied change before failure: changes=%d updated=%v err=%v", len(changes), updated, err)
	}
	delete(service.failUpdates, "source/b")
	if rollbackErr := rollbackWikiContentChanges(context.Background(), service, changes); rollbackErr != nil {
		t.Fatalf("rollback failed: %v", rollbackErr)
	}
	if got := service.pages["source/a"].Content; got != "see [[concept/old]]" {
		t.Fatalf("rollback content = %q", got)
	}
	if service.updateCalls != 3 {
		t.Fatalf("UpdateAutoLinkedContent calls = %d, want apply + failed apply + rollback", service.updateCalls)
	}
}
