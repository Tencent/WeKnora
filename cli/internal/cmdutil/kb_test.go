package cmdutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/Tencent/WeKnora/client"
)

type fakeKBLister struct {
	kbs []sdk.KnowledgeBase
	err error
}

func (f *fakeKBLister) ListKnowledgeBases(context.Context) ([]sdk.KnowledgeBase, error) {
	return f.kbs, f.err
}

func TestResolveKBNameToIDSingleMatch(t *testing.T) {
	lister := &fakeKBLister{kbs: []sdk.KnowledgeBase{
		{ID: "id-a", Name: "prod"},
		{ID: "id-b", Name: "staging"},
	}}
	got, err := ResolveKBNameToID(context.Background(), lister, "prod")
	if err != nil {
		t.Fatalf("ResolveKBNameToID() error = %v", err)
	}
	if got != "id-a" {
		t.Errorf("ResolveKBNameToID() = %q, want id-a", got)
	}
}

// KB names are not unique - knowledge_bases has no unique index and
// CreateKnowledgeBase only rejects an empty name - so a duplicate name must
// fail loudly instead of writing into whichever knowledge base was listed
// first.
func TestResolveKBNameToIDRejectsDuplicateNames(t *testing.T) {
	lister := &fakeKBLister{kbs: []sdk.KnowledgeBase{
		{ID: "id-a", Name: "prod"},
		{ID: "id-b", Name: "prod"},
	}}
	_, err := ResolveKBNameToID(context.Background(), lister, "prod")
	if err == nil {
		t.Fatal("ResolveKBNameToID() = nil error, want input.invalid_argument")
	}
	typed := AsError(err)
	if typed == nil {
		t.Fatalf("ResolveKBNameToID() = %v, want a *Error", err)
	}
	if typed.Code != CodeInputInvalidArgument {
		t.Errorf("code = %s, want %s", typed.Code, CodeInputInvalidArgument)
	}
	if !strings.Contains(typed.Message, "2 knowledge bases") {
		t.Errorf("message = %q, want it to name the number of matches", typed.Message)
	}
	if typed.Hint == "" {
		t.Error("hint is empty, want a pointer to `weknora kb list`")
	}
}

func TestResolveKBNameToIDNotFound(t *testing.T) {
	lister := &fakeKBLister{kbs: []sdk.KnowledgeBase{{ID: "id-a", Name: "prod"}}}
	_, err := ResolveKBNameToID(context.Background(), lister, "nope")
	typed := AsError(err)
	if typed == nil || typed.Code != CodeKBNotFound {
		t.Fatalf("code = %v, want %s", err, CodeKBNotFound)
	}
}

func TestResolveKBFlagPassesIDsThrough(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	// A lister that fails proves the id path never lists.
	lister := &fakeKBLister{err: errors.New("must not be called")}
	got, err := ResolveKBFlag(context.Background(), lister, id)
	if err != nil {
		t.Fatalf("ResolveKBFlag(%q) error = %v", id, err)
	}
	if got != id {
		t.Errorf("ResolveKBFlag(%q) = %q, want it unchanged", id, got)
	}
}

func TestResolveKBFlagResolvesNames(t *testing.T) {
	lister := &fakeKBLister{kbs: []sdk.KnowledgeBase{{ID: "id-a", Name: "prod"}}}
	got, err := ResolveKBFlag(context.Background(), lister, "prod")
	if err != nil {
		t.Fatalf("ResolveKBFlag() error = %v", err)
	}
	if got != "id-a" {
		t.Errorf("ResolveKBFlag() = %q, want id-a", got)
	}
}
