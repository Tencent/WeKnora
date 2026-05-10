package cmdutil

import (
	"context"
	"fmt"

	sdk "github.com/Tencent/WeKnora/client"
)

// KBLister is the narrow SDK surface ResolveKBNameToID depends on. The
// production *sdk.Client satisfies it; tests inject fakes without standing
// up an HTTP server.
type KBLister interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// ResolveKBNameToID looks up a knowledge base by name and returns its ID.
// Used by `init`, `link`, and `Factory.ResolveKB` — a single lookup so the
// match policy (currently exact case-sensitive) lives in one place.
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
