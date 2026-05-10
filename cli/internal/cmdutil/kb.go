package cmdutil

import (
	"context"
	"fmt"
	"regexp"

	sdk "github.com/Tencent/WeKnora/client"
)

// uuidPattern matches the canonical 8-4-4-4-12 UUID form. WeKnora's KB ids
// are uuid.New().String() output stored as varchar(36); names are arbitrary
// user-supplied strings, so format-detection is unambiguous.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsKBID reports whether s looks like a KB id. Used by Factory.ResolveKB and
// any caller that accepts a single id-or-name selector value — same pattern
// gcloud uses for --project (id vs name auto-detection).
func IsKBID(s string) bool { return uuidPattern.MatchString(s) }

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
