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
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeUpdateSvc struct {
	current   *sdk.Knowledge
	getErr    error
	updateErr error
	got       *sdk.Knowledge // captured object passed to UpdateKnowledge
}

func (f *fakeUpdateSvc) GetKnowledge(_ context.Context, id string) (*sdk.Knowledge, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	c := *f.current
	c.ID = id
	return &c, nil
}

func (f *fakeUpdateSvc) UpdateKnowledge(_ context.Context, k *sdk.Knowledge) error {
	f.got = k
	return f.updateErr
}

func strptr(s string) *string { return &s }

func TestDocUpdate_Title_FetchThenUpdate(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{current: &sdk.Knowledge{Title: "Old", Description: "keep", FileName: "f.md"}}
	require.NoError(t, runUpdate(context.Background(),
		&UpdateOptions{Title: strptr("New")}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "doc_abc"))
	// Only title changed; description preserved from the fetched record.
	require.NotNil(t, svc.got)
	assert.Equal(t, "New", svc.got.Title)
	assert.Equal(t, "keep", svc.got.Description)
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.OK)
	assert.Equal(t, "doc_abc", env.Data.ID)
	assert.Equal(t, "New", env.Data.Title)
}

func TestDocUpdate_DescriptionOnly_PreservesTitle(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{current: &sdk.Knowledge{Title: "Keep", Description: "old"}}
	require.NoError(t, runUpdate(context.Background(),
		&UpdateOptions{Description: strptr("fresh")}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "doc_abc"))
	assert.Equal(t, "Keep", svc.got.Title)
	assert.Equal(t, "fresh", svc.got.Description)
}

func TestDocUpdate_NoFlags_MissingFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{current: &sdk.Knowledge{}}
	err := runUpdate(context.Background(), &UpdateOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "doc_abc")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
	assert.Nil(t, svc.got, "must not call UpdateKnowledge when no flag is set")
}

func TestDocUpdate_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{getErr: errors.New("HTTP error 404: not found")}
	err := runUpdate(context.Background(),
		&UpdateOptions{Title: strptr("x")}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "missing")
	require.Error(t, err)
	assert.True(t, cmdutil.IsNotFound(err))
}

// TestDocUpdate_DryRun_NoServerCall: --dry-run emits a doc.update plan (exit 0)
// without reaching the server.
func TestDocUpdate_DryRun_NoServerCall(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdUpdate(docDryRunFactory(t)), "doc_x", "--title", "T", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "dry-run must succeed without a client")
	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.Meta.DryRun)
	assert.Equal(t, "doc.update", env.Meta.Plan["action"])
}

// TestDocUpdate_RequiresConfirmation asserts that without -y (non-TTY / JSON
// mode), doc update returns input.confirmation_required (exit 10) — parity with
// kb/agent update gating (AGENTS.md §3.1: all three updates are confirmation-gated).
func TestDocUpdate_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t) // non-TTY
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return nil, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarnessDoc(NewCmdUpdate(f), "doc_abc", "--title", "New", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	// retry argv must include -y and the target id
	assert.Contains(t, ce.RetryArgv, "-y")
	assert.Contains(t, ce.RetryArgv, "doc_abc")
}

// TestDocUpdate_DryRun_RejectsNoFlag: --dry-run rejects the no-mutation-flag
// invocation identically to the live path (validation before the dry-run gate).
func TestDocUpdate_DryRun_RejectsNoFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdUpdate(docDryRunFactory(t)), "doc_x", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject identically to live path")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}
