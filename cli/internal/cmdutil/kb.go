package cmdutil

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/Tencent/WeKnora/client"
)

// kbIDPrefix is the server-emitted prefix that marks a string as a KB id
// (rather than a KB name). The single `--kb` flag uses it for client-side
// id-vs-name auto-detection — the same pattern gcloud uses for --project
// (numeric id vs project-id form).
const kbIDPrefix = "kb_"

// IsKBID reports whether s looks like a KB id. Used by Factory.ResolveKB and
// any caller that accepts a single id-or-name selector value.
func IsKBID(s string) bool { return strings.HasPrefix(s, kbIDPrefix) }

// KBLister is the narrow SDK surface ResolveKBNameToID depends on. The
// production *sdk.Client satisfies it; tests inject fakes without standing
// up an HTTP server.
type KBLister interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// ResolveKBNameToID looks up a knowledge base by name and returns its ID.
// Used by `link` and `Factory.ResolveKB` — a single lookup so the match
// policy (currently exact case-sensitive) lives in one place.
func ResolveKBNameToID(ctx context.Context, lister KBLister, name string) (string, error) {
	kbs, err := lister.ListKnowledgeBases(ctx)
	if err != nil {
		return "", Wrapf(ClassifyHTTPError(err), err, "list knowledge bases")
	}
	for _, kb := range kbs {
		if kb.Name == name {
			return kb.ID, nil
		}
	}
	return "", NewError(CodeKBNotFound, fmt.Sprintf("knowledge base not found: %s", name))
}
