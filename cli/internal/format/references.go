package format

import (
	"fmt"
	"io"

	sdk "github.com/Tencent/WeKnora/client"
)

// WriteReferences renders the compact references footer used by chat and
// agent invoke: a horizontal rule, one numbered line per reference,
// best-available title + optional score. Skipped entirely when refs is
// empty - agent-friendly silence beats an empty banner.
func WriteReferences(w io.Writer, refs []*sdk.SearchResult) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "──── References ────")
	for i, r := range refs {
		if r == nil {
			continue
		}
		title := r.KnowledgeTitle
		if title == "" {
			title = r.KnowledgeFilename
		}
		if title == "" {
			title = r.KnowledgeID
		}
		fmt.Fprintf(w, "[%d] %s", i+1, title)
		if r.Score > 0 {
			fmt.Fprintf(w, "  score=%.3f", r.Score)
		}
		fmt.Fprintln(w)
	}
}
