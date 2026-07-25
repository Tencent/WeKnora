package cmdutil

import (
	"context"
	"fmt"

	sdk "github.com/Tencent/WeKnora/client"
)

// ModelLister is the narrow SDK surface ResolveModelRef needs. *sdk.Client
// satisfies it.
type ModelLister interface {
	ListModels(ctx context.Context) ([]sdk.Model, error)
}

// ResolveModelRef returns ref unchanged when it is already a model id (UUID);
// otherwise it resolves ref as a model NAME among models of wantType (pass ""
// to match any type) and returns that model's id. Mirrors the id-or-name policy
// of --kb so model-referencing flags accept either.
//
// Errors with resource.not_found when nothing matches, and
// input.invalid_argument when the name is ambiguous (so the caller passes an id
// instead). The empty ref is returned as-is for callers to treat as "unset".
func ResolveModelRef(ctx context.Context, lister ModelLister, ref, wantType string) (string, error) {
	if ref == "" || IsKBID(ref) { // IsKBID is a generic UUID check
		return ref, nil
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return "", WrapHTTP(err, "list models to resolve %q", ref)
	}
	var matches []sdk.Model
	for _, m := range models {
		if m.Name == ref && (wantType == "" || string(m.Type) == wantType) {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		msg := fmt.Sprintf("no model named %q", ref)
		if wantType != "" {
			msg = fmt.Sprintf("no %s model named %q", wantType, ref)
		}
		return "", NewError(CodeResourceNotFound, msg).
			WithHint("discover models with `weknora model list`")
	default:
		return "", NewError(CodeInputInvalidArgument,
			fmt.Sprintf("%q matches %d models — pass the model id instead", ref, len(matches))).
			WithHint("`weknora model list` shows the ids")
	}
}
