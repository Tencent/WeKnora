package tools

import (
	"strings"

	customknowledge "github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/types"
)

const protectedKnowledgeV2MutationError = "V2 knowledge pages are system-owned; use wiki_write_page with verified production provenance"

func isProtectedKnowledgeV2Page(page *types.WikiPage) bool {
	return page != nil && (strings.HasPrefix(strings.TrimSpace(page.Slug), "video/") ||
		customknowledge.IsWikiObjectCandidate(page.Content))
}
