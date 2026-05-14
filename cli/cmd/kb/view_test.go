package kb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeGetSvc struct {
	kb  *sdk.KnowledgeBase
	err error
}

func (f *fakeGetSvc) GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error) {
	return f.kb, f.err
}

func TestGet_OK_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{
		ID: "kb1", Name: "Marketing", KnowledgeCount: 12, ChunkCount: 245,
	}}
	if err := runView(context.Background(), &ViewOptions{}, nil, svc, "kb1"); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID:", "kb1", "NAME:", "Marketing", "DOCS:", "12 docs", "CHUNKS:", "245 chunks"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGet_OK_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "Marketing"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.JSONOptions{}, svc, "kb1"); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(strings.TrimSpace(got), `{"id":"kb1"`) {
		t.Errorf("expected bare object starting with id, got %q", got)
	}
	if strings.Contains(got, `"ok":true`) {
		t.Errorf("bare output must not carry envelope keys, got %q", got)
	}
}

func TestGet_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeGetSvc{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &ViewOptions{}, nil, svc, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}
