package modelcmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeViewSvc struct {
	model *sdk.Model
	err   error
}

func (f *fakeViewSvc) GetModel(_ context.Context, _ string) (*sdk.Model, error) {
	return f.model, f.err
}

func TestModelView_Text(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{model: &sdk.Model{
		ID: "m1", DisplayName: "GPT-X", Type: sdk.ModelTypeKnowledgeQA,
		Source: sdk.ModelSourceOpenAI, IsDefault: true,
	}}
	if err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "m1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID:", "m1", "NAME:", "GPT-X", "TYPE:", "KnowledgeQA", "SOURCE:", "openai", "DEFAULT:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestModelView_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{model: &sdk.Model{ID: "m1", Type: sdk.ModelTypeEmbedding}}
	if err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "m1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	var env struct {
		OK   bool      `json:"ok"`
		Data sdk.Model `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if !env.OK || env.Data.ID != "m1" {
		t.Errorf("expected ok envelope with id=m1, got %q", out.String())
	}
}

func TestModelView_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewSvc{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}
