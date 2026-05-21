package agentcmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeEditSvc scripts GetAgent (fetch baseline) + UpdateAgent (apply
// surgical overlays). updateCalls lets tests verify that no-flag invocations
// don't reach the wire.
type fakeEditSvc struct {
	getResp     *sdk.Agent
	getErr      error
	updateReq   *sdk.UpdateAgentRequest
	updateID    string
	updateResp  *sdk.Agent
	updateErr   error
	updateCalls int
}

func (f *fakeEditSvc) GetAgent(_ context.Context, _ string) (*sdk.Agent, error) {
	return f.getResp, f.getErr
}

func (f *fakeEditSvc) UpdateAgent(_ context.Context, id string, req *sdk.UpdateAgentRequest) (*sdk.Agent, error) {
	f.updateReq = req
	f.updateID = id
	f.updateCalls++
	return f.updateResp, f.updateErr
}

func TestEdit_FetchThenUpdate_PreservesUntouchedFields(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp: &sdk.Agent{
			ID: "ag_abc", Name: "Original", Description: "Keep me",
			Config: &sdk.AgentConfig{ModelID: "model-x", Temperature: 0.7, KnowledgeBases: []string{"kb_a"}},
		},
		updateResp: &sdk.Agent{ID: "ag_abc"},
	}
	// Only --description passed; everything else should round-trip.
	opts := &EditOptions{
		AgentID:     "ag_abc",
		Description: "Updated",
		flags:       editFlagSet{descriptionSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.updateReq)
	assert.Equal(t, "Original", svc.updateReq.Name, "Name must round-trip unchanged")
	assert.Equal(t, "Updated", svc.updateReq.Description)
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, "model-x", svc.updateReq.Config.ModelID, "ModelID must round-trip")
	assert.Equal(t, []string{"kb_a"}, svc.updateReq.Config.KnowledgeBases, "KBs must round-trip")
	assert.InDelta(t, 0.7, svc.updateReq.Config.Temperature, 0.001)
}

func TestEdit_AddRemoveKB_SameID_NetNoOpWithWarning(t *testing.T) {
	_, errBuf := iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Config: &sdk.AgentConfig{KnowledgeBases: []string{"kb_a"}}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:   "ag_abc",
		AddKBs:    []string{"kb_b"},
		RemoveKBs: []string{"kb_b"},
		flags:     editFlagSet{addKBsSet: true, removeKBsSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, []string{"kb_a"}, svc.updateReq.Config.KnowledgeBases, "net no-op preserves original list")
	assert.Contains(t, errBuf.String(), "cancel out", "warning emitted to stderr")
}

func TestEdit_NoFlags_InvalidArgument(t *testing.T) {
	svc := &fakeEditSvc{}
	err := runEdit(context.Background(), &EditOptions{AgentID: "ag_abc"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Equal(t, 0, svc.updateCalls, "must not call UpdateAgent")
}

func TestEdit_AddKB_AlreadyAttached_Silent(t *testing.T) {
	_, errBuf := iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Config: &sdk.AgentConfig{KnowledgeBases: []string{"kb_a", "kb_b"}}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID: "ag_abc",
		AddKBs:  []string{"kb_a"}, // already attached
		flags:   editFlagSet{addKBsSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, []string{"kb_a", "kb_b"}, svc.updateReq.Config.KnowledgeBases, "no duplicate")
	assert.NotContains(t, errBuf.String(), "warning", "already-attached is silent success")
}

func TestEdit_RemoveKB_Unattached_Silent(t *testing.T) {
	_, errBuf := iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Config: &sdk.AgentConfig{KnowledgeBases: []string{"kb_a"}}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:   "ag_abc",
		RemoveKBs: []string{"kb_zzz"},
		flags:     editFlagSet{removeKBsSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, []string{"kb_a"}, svc.updateReq.Config.KnowledgeBases)
	assert.NotContains(t, errBuf.String(), "warning")
}

func TestEdit_ClearDescription_EmptyString(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Name: "X", Description: "old", Config: &sdk.AgentConfig{ModelID: "m"}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:     "ag_abc",
		Description: "",
		flags:       editFlagSet{descriptionSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, "", svc.updateReq.Description, "explicit empty must clear server-side")
	assert.Equal(t, "X", svc.updateReq.Name, "Name round-trip unchanged")
}

func TestEdit_ConfigFile_OverridesByFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp: &sdk.Agent{
			Name:   "X",
			Config: &sdk.AgentConfig{ModelID: "old-model", Temperature: 0.1},
		},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:        "ag_abc",
		Temperature:    0.9,
		ConfigFileBody: bytes.NewBufferString(`{"temperature":0.5,"model_id":"file-model"}`),
		ConfigFileKind: "json",
		flags:          editFlagSet{temperatureSet: true, configFileSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, "file-model", svc.updateReq.Config.ModelID, "file overrides current state")
	assert.InDelta(t, 0.9, svc.updateReq.Config.Temperature, 0.001, "flag overrides file")
}

// TestEdit_ConfigFile_FullReplacesBaseline pins the documented behavior:
// --config-file fully replaces the AgentConfig baseline; current-server
// fields not mentioned in the file are zeroed. The Long help directs
// users to surgical flags when they want a partial update.
func TestEdit_ConfigFile_FullReplacesBaseline(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp: &sdk.Agent{
			Name: "X",
			Config: &sdk.AgentConfig{
				SystemPrompt:   "Existing prompt",
				ModelID:        "old-model",
				Temperature:    0.1,
				AgentMode:      "smart-reasoning",
				KnowledgeBases: []string{"kb_existing"},
			},
		},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:        "ag_abc",
		ConfigFileBody: bytes.NewBufferString(`{"model_id":"file-only"}`),
		ConfigFileKind: "json",
		flags:          editFlagSet{configFileSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, "file-only", svc.updateReq.Config.ModelID, "file's model_id applied")
	assert.Equal(t, "", svc.updateReq.Config.SystemPrompt, "file fully replaces baseline; unset fields are zeroed")
	assert.InDelta(t, 0.0, svc.updateReq.Config.Temperature, 0.001, "unset fields zeroed")
	assert.Equal(t, "", svc.updateReq.Config.AgentMode, "unset fields zeroed")
	assert.Empty(t, svc.updateReq.Config.KnowledgeBases, "unset fields zeroed")
}

func TestEdit_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{getErr: errBadHTTP404}
	opts := &EditOptions{AgentID: "ag_missing", Name: "x", flags: editFlagSet{nameSet: true}}
	err := runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource.not_found")
}

func TestEdit_AddKB_AppendsToExisting(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Config: &sdk.AgentConfig{KnowledgeBases: []string{"kb_a"}}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID: "ag_abc",
		AddKBs:  []string{"kb_b", "kb_c"},
		flags:   editFlagSet{addKBsSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, []string{"kb_a", "kb_b", "kb_c"}, svc.updateReq.Config.KnowledgeBases)
}

func TestEdit_Temperature_Bounds(t *testing.T) {
	for _, badT := range []float64{-0.1, 2.1, 100.0} {
		t.Run(fmt.Sprintf("t=%g", badT), func(t *testing.T) {
			cmd := NewCmdEdit(nil)
			cmd.SetArgs([]string{"ag_abc", "--temperature", fmt.Sprintf("%f", badT)})
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			require.Error(t, err, "expected error for --temperature %g", badT)
			assert.Contains(t, err.Error(), "0.0..2.0")
		})
	}
}

func TestEdit_SystemPromptFile(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		getResp:    &sdk.Agent{Config: &sdk.AgentConfig{ModelID: "m"}},
		updateResp: &sdk.Agent{},
	}
	opts := &EditOptions{
		AgentID:            "ag_abc",
		SystemPromptReader: strings.NewReader("new prompt\n"),
		flags:              editFlagSet{systemPromptSet: true},
	}
	require.NoError(t, runEdit(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, "new prompt", svc.updateReq.Config.SystemPrompt)
}

// withRootHarnessAgent wraps `weknora agent edit ...` under a synthetic root
// cmd that registers the global persistent flags (mirrors addGlobalFlags in
// cmd/root.go).
func withRootHarnessAgent(edit *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "Output format: human | json | ndjson")
	pf.StringP("jq", "q", "", "")
	ag := &cobra.Command{Use: "agent"}
	ag.AddCommand(edit)
	root.AddCommand(ag)
	root.SetArgs(append([]string{"agent", "edit"}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

// TestAgentEdit_RequiresConfirmation asserts that without -y (non-TTY / JSON
// mode), agent edit returns input.confirmation_required (exit 10).
func TestAgentEdit_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t) // non-TTY
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return nil, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarnessAgent(NewCmdEdit(f), "ag_abc", "--name", "Renamed", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err), "exit code 10 per destructive-write protocol")
	// retry command must include -y and the agent id
	assert.Contains(t, ce.RetryCommand, "-y")
	assert.Contains(t, ce.RetryCommand, "ag_abc")
}
