package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// applyRejectedAliases keeps a person's alias removals in force. A user edit
// or revert records what it removed and lifts the rejection for anything it
// restores; every other writer has rejected aliases stripped from its write.
func applyRejectedAliases(ctx context.Context, stored, page *types.WikiPage) error {
	previous := stored.RejectedAliases()
	rejected := previous
	switch types.WikiEditSourceFromContext(ctx) {
	case types.WikiEditSourceUser, types.WikiEditSourceRevert:
		rejected = updateRejectedAliases(previous, stored.Aliases, page.Aliases)
	default:
		page.Aliases = dropRejectedAliases(page.Aliases, previous)
	}
	if len(previous) == 0 && len(rejected) == 0 {
		return nil
	}
	return page.SetRejectedAliases(rejected)
}

func updateRejectedAliases(rejected []string, before, after types.StringArray) []string {
	kept := make(map[string]bool, len(after))
	for _, alias := range after {
		kept[types.NormalizeWikiAlias(alias)] = true
	}
	seen := make(map[string]bool, len(rejected)+len(before))
	out := make([]string, 0, len(rejected)+len(before))
	for _, group := range [][]string{rejected, before} {
		for _, alias := range group {
			key := types.NormalizeWikiAlias(alias)
			if key == "" || kept[key] || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, alias)
		}
	}
	return out
}

func dropRejectedAliases(aliases types.StringArray, rejected []string) types.StringArray {
	if len(rejected) == 0 {
		return aliases
	}
	blocked := make(map[string]bool, len(rejected))
	for _, alias := range rejected {
		blocked[types.NormalizeWikiAlias(alias)] = true
	}
	out := make(types.StringArray, 0, len(aliases))
	for _, alias := range aliases {
		if !blocked[types.NormalizeWikiAlias(alias)] {
			out = append(out, alias)
		}
	}
	return out
}

// aliasesNamingOtherPages returns the normalized candidate aliases that are
// the title of a different live page. Such an alias would send links and
// annotations for that page's name to this one. A failed lookup keeps the
// aliases: a transient database error must not fail the whole reduce.
func (s *wikiIngestService) aliasesNamingOtherPages(
	ctx context.Context, kbID, slug string, additions []SlugUpdate,
) map[string]bool {
	var candidates []string
	for _, add := range additions {
		candidates = append(candidates, add.Item.Aliases...)
	}
	if len(candidates) == 0 {
		return nil
	}
	titles, err := s.wikiService.FindTitlesOnOtherPages(ctx, kbID, slug, candidates)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: alias title lookup failed for %s: %v", slug, err)
		return nil
	}
	out := make(map[string]bool, len(titles))
	for _, title := range titles {
		out[types.NormalizeWikiAlias(title)] = true
	}
	return out
}
