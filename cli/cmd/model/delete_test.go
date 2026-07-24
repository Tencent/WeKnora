package modelcmd

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

// modelDryRunFactory builds a Factory whose Client / Prompter closures fail the
// test if invoked — used by paths (flag validation, dry-run) that must not
// reach the SDK. For the confirmation test, a permissive factory is built inline.
func modelDryRunFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("path must not call Factory.Client()")
			return nil, nil
		},
		Prompter: func() prompt.Prompter {
			t.Fatal("path must not call Factory.Prompter()")
			return nil
		},
	}
}

type fakeDeleteSvc struct {
	gotID string
	err   error
}

func (f *fakeDeleteSvc) DeleteModel(_ context.Context, id string) error {
	f.gotID = id
	return f.err
}

func TestModelDelete_CallsSDKAndEmits(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	require.NoError(t, runDelete(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "model_abc"))
	assert.Equal(t, "model_abc", svc.gotID)
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.OK)
	assert.Equal(t, "model_abc", env.Data.ID)
	assert.True(t, env.Data.Deleted)
}

func TestModelDelete_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "missing")
	require.Error(t, err)
	assert.True(t, cmdutil.IsNotFound(err))
}

// TestModelDelete_RequiresConfirmation: without -y (non-TTY/JSON), delete exits
// 10 (input.confirmation_required) and never reaches the SDK.
func TestModelDelete_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t)
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return nil, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarnessModel(NewCmdDelete(f), "model_abc", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.Contains(t, ce.RetryArgv, "-y")
}

// TestModelDelete_DryRun: --dry-run previews without reaching the SDK.
func TestModelDelete_DryRun(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdDelete(modelDryRunFactory(t)), "model_abc", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute())
	var env struct {
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.Meta.DryRun)
	assert.Equal(t, "model.delete", env.Meta.Plan["action"])
}
