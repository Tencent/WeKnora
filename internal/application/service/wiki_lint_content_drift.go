package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiLintDriftMinSourceRows is the smallest number of distinct row identities
// a page's source chunks must carry before the drift check says anything.
//
// Below it the check has no signal: three source rows that a page merges into
// one is a 67% "loss" that is really an editor collapsing a two-line list, and
// the write guard's 20% tolerance cannot tell the two apart on a sample that
// small. Requiring five rows keeps the report about tables that actually
// enumerate entities.
const wikiLintDriftMinSourceRows = 5

// wikiLintDriftMaxChunksPerWindow caps how many distinct source chunks one
// lint window resolves into memory. lintCursorBatch already bounds the pages;
// this bounds the other axis, the cited chunks per window, at 2000 ids per
// single ListChunksByID call — enough for a window of chunk-citing pages while
// keeping the returned chunk bodies and the IN-list bounded.
const wikiLintDriftMaxChunksPerWindow = 2000

// wikiLintDriftExampleLimit caps how many missing row identities a drift issue
// names. The report lists identities, never whole tables.
const wikiLintDriftExampleLimit = 3

// wikiLintContentDrift compares pageContent's table rows against the rows of
// the chunks the page was built from. It returns the identities present in the
// source chunks but absent from the page, and the number of distinct source
// identities (the union across chunks, so a row several chunks share counts
// once).
//
// Row identity, normalization and the header/delimiter rules are exactly
// wikiWriteTableRowIdentities', so lint and the write-time guard agree on what
// a lost row is.
func wikiLintContentDrift(pageContent string, sourceChunks []*types.Chunk) (missing []string, total int) {
	source := make(map[string]struct{})
	for _, chunk := range sourceChunks {
		if chunk == nil {
			continue
		}
		for _, identity := range wikiWriteTableRowIdentities(chunk.Content) {
			source[identity] = struct{}{}
		}
	}
	total = len(source)
	if total == 0 {
		return nil, 0
	}

	present := make(map[string]struct{})
	for _, identity := range wikiWriteTableRowIdentities(pageContent) {
		present[identity] = struct{}{}
	}
	for identity := range source {
		if _, ok := present[identity]; ok {
			continue
		}
		missing = append(missing, identity)
	}
	if len(missing) == 0 {
		return nil, total
	}
	// Sorted so the issue text is stable run to run; the source union is a map.
	sort.Strings(missing)
	return missing, total
}

// wikiLintIsContentDrift reports whether the missing rows are enough to be
// worth an issue: the source must be large enough to trust, and the share lost
// must be strictly above the write guard's ingest tolerance.
func wikiLintIsContentDrift(missing []string, total int) bool {
	if total < wikiLintDriftMinSourceRows {
		return false
	}
	return wikiWriteRowIdentityLossRatio(len(missing), total) > wikiWriteIngestRowIdentityLossLimit
}

// wikiLintContentDriftDescription names the page, the size of the loss and a
// few example row identities — never the rows' full content.
func wikiLintContentDriftDescription(title string, missing []string, total int) string {
	examples := missing
	if len(examples) > wikiLintDriftExampleLimit {
		examples = examples[:wikiLintDriftExampleLimit]
	}
	return fmt.Sprintf(
		"Page '%s' is missing %d of %d table rows its source documents contain (e.g. %s)",
		title, len(missing), total, strings.Join(examples, ", "),
	)
}

// wikiLintContentDriftIssues evaluates one lint window: it resolves the union
// of the window's cited chunk ids in a single batch, then reports every page
// whose content dropped too much of its own sources' table rows.
//
// A page with no ChunkRefs is skipped: those refs are the page's precise
// sources (summary pages carry document-level refs only), so without them
// there is no source content to compare against and any document-level guess
// would be a different, noisier rule.
func (s *WikiLintService) wikiLintContentDriftIssues(
	ctx context.Context, tenantID uint64, pages []*types.WikiPage,
) []WikiLintIssue {
	if s.chunkRepo == nil {
		return nil
	}

	ids := make([]string, 0, len(pages))
	seen := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		if page == nil || len(page.ChunkRefs) == 0 {
			continue
		}
		for _, id := range page.ChunkRefs {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > wikiLintDriftMaxChunksPerWindow {
		// Skip the whole window rather than a partial batch: a truncated source
		// set would under-count a page's rows and turn the ratio into noise.
		logger.Warnf(ctx,
			"wiki lint: content drift skipped for a %d-page window: %d cited chunks exceed the %d budget",
			len(pages), len(ids), wikiLintDriftMaxChunksPerWindow)
		return nil
	}

	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, ids)
	if err != nil {
		logger.Warnf(ctx,
			"wiki lint: content drift skipped for a %d-page window: list chunks: %v", len(pages), err)
		return nil
	}
	byID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			byID[chunk.ID] = chunk
		}
	}

	var issues []WikiLintIssue
	for _, page := range pages {
		if page == nil || len(page.ChunkRefs) == 0 {
			continue
		}
		sourceChunks := make([]*types.Chunk, 0, len(page.ChunkRefs))
		for _, id := range page.ChunkRefs {
			if chunk, ok := byID[id]; ok {
				sourceChunks = append(sourceChunks, chunk)
			}
		}
		missing, total := wikiLintContentDrift(page.Content, sourceChunks)
		if !wikiLintIsContentDrift(missing, total) {
			continue
		}
		issues = append(issues, WikiLintIssue{
			Type:        LintIssueContentDrift,
			Severity:    SeverityWarning,
			PageSlug:    page.Slug,
			Description: wikiLintContentDriftDescription(page.Title, missing, total),
			AutoFixable: false,
		})
	}
	return issues
}
