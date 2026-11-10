package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiContentRewrite func(string) (string, bool)

type appliedWikiContentChange struct {
	page            *types.WikiPage
	originalContent string
}

// applyIncomingWikiContentRewrite updates machine-maintained links without
// incrementing user-visible page versions. It stops on the first failure and
// returns the already-applied changes so the caller can compensate.
func applyIncomingWikiContentRewrite(
	ctx context.Context,
	service interfaces.WikiPageService,
	kbID string,
	inLinks []string,
	rewrite wikiContentRewrite,
) ([]appliedWikiContentChange, []string, error) {
	var changes []appliedWikiContentChange
	var updatedSlugs []string
	for _, sourceSlug := range dedupNonEmptyStrings(inLinks) {
		page, err := service.GetPageBySlug(ctx, kbID, sourceSlug)
		if err != nil || page == nil {
			if err == nil {
				err = fmt.Errorf("empty result")
			}
			return changes, updatedSlugs, fmt.Errorf("load incoming page %s: %w", sourceSlug, err)
		}
		updatedContent, changed := rewrite(page.Content)
		if !changed {
			continue
		}
		original := page.Content
		page.Content = updatedContent
		if err := service.UpdateAutoLinkedContent(ctx, page); err != nil {
			page.Content = original
			return changes, updatedSlugs, fmt.Errorf("update incoming page %s: %w", sourceSlug, err)
		}
		changes = append(changes, appliedWikiContentChange{page: page, originalContent: original})
		updatedSlugs = append(updatedSlugs, sourceSlug)
	}
	return changes, updatedSlugs, nil
}

func rollbackWikiContentChanges(
	ctx context.Context,
	service interfaces.WikiPageService,
	changes []appliedWikiContentChange,
) error {
	var failures []string
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		change.page.Content = change.originalContent
		if err := service.UpdateAutoLinkedContent(ctx, change.page); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", change.page.Slug, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to roll back incoming pages: %s", strings.Join(failures, "; "))
	}
	return nil
}

func joinWikiMutationErrors(primary error, extras ...error) string {
	parts := []string{primary.Error()}
	for _, err := range extras {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	return strings.Join(parts, "; ")
}
