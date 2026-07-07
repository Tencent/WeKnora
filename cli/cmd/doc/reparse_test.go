package doc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeReparseSvc struct {
	k      *sdk.Knowledge
	err    error
	called bool
}

func (f *fakeReparseSvc) ReparseKnowledge(_ context.Context, id string) (*sdk.Knowledge, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	k := *f.k
	k.ID = id
	return &k, nil
}

func TestDocReparse_Text(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeReparseSvc{k: &sdk.Knowledge{FileName: "runbook.md", ParseStatus: "processing"}}
	require.NoError(t, runReparse(context.Background(), &ReparseOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "doc_abc"))
	assert.True(t, svc.called)
	got := out.String()
	assert.Contains(t, got, "doc_abc")
	assert.Contains(t, got, "runbook.md")
	assert.Contains(t, got, "processing")
	assert.Contains(t, got, "doc wait doc_abc")
}

func TestDocReparse_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeReparseSvc{k: &sdk.Knowledge{ParseStatus: "processing"}}
	require.NoError(t, runReparse(context.Background(), &ReparseOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "doc_abc"))
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.OK)
	assert.Equal(t, "doc_abc", env.Data.ID)
	assert.Equal(t, "processing", env.Data.ParseStatus)
}

func TestDocReparse_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeReparseSvc{err: errors.New("HTTP error 404: not found")}
	err := runReparse(context.Background(), &ReparseOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "missing")
	require.Error(t, err)
	assert.True(t, cmdutil.IsNotFound(err))
}

// TestDocReparse_DryRun_NoServerCall: --dry-run must emit a doc.reparse plan
// (exit 0) without reaching the server, honoring the mutation-preview contract.
func TestDocReparse_DryRun_NoServerCall(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdReparse(docDryRunFactory(t)), "doc_x", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "dry-run must succeed without a client")
	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.OK)
	assert.True(t, env.Meta.DryRun)
	assert.Equal(t, "doc.reparse", env.Meta.Plan["action"])
}
