// Package search implements the `weknora search` command tree:
// chunks / kb / docs / sessions.
package search

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmdSearch builds the `weknora search` parent. Pure dispatcher to the
// four subcommands - users must pick a verb (chunks / kb / docs / sessions).
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search across chunks, knowledge bases, documents, or sessions",
		Long: `Verb-noun search tree:

  search chunks   "<q>" --kb X     hybrid retrieval (RAG search)
  search kb       "<q>"            find KBs by name / description
  search docs     "<q>" --kb X     find documents inside a KB
  search sessions "<q>"            find chat sessions by title / description`,
		Example: `  weknora search chunks "what is RAG?" --kb engineering
  weknora search kb     "marketing"
  weknora search docs   "Q3 forecast" --kb finance
  weknora search sessions "onboarding"`,
	}

	cmd.AddCommand(NewCmdChunks(f))
	cmd.AddCommand(NewCmdKB(f))
	cmd.AddCommand(NewCmdDocs(f))
	cmd.AddCommand(NewCmdSessions(f))
	return cmd
}

// emptyContentSearchHint returns an actionable note when a KB-scoped content
// search (chunks / docs) yields zero results, so an agent can distinguish
// "no match" from "the KB has no indexed content". Empty when n > 0 so it
// never adds noise to real results.
func emptyContentSearchHint(n int) string {
	if n > 0 {
		return ""
	}
	return "0 results: this may be no match, OR the KB has no indexed chunks. " +
		"Check `weknora kb status <kb>` (chunk_count) and `weknora doc list --kb <kb>` (parse_status); " +
		"documents in parse_status=draft are not indexed — run `weknora doc reparse <doc-id>`."
}
