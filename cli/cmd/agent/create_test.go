package agentcmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeCreateSvc records all three SDK methods this command may invoke.
type fakeCreateSvc struct {
	createReq    *sdk.CreateAgentRequest
	createResp   *sdk.Agent
	createErr    error
	copySrcID    string
	copyResp     *sdk.Agent
	copyErr      error
	updateID     string
	updateReq    *sdk.UpdateAgentRequest
	updateResp   *sdk.Agent
	updateErr    error
	updateCalled bool
}

func (f *fakeCreateSvc) CreateAgent(_ context.Context, req *sdk.CreateAgentRequest) (*sdk.Agent, error) {
	f.createReq = req
	return f.createResp, f.createErr
}
func (f *fakeCreateSvc) CopyAgent(_ context.Context, id string) (*sdk.Agent, error) {
	f.copySrcID = id
	return f.copyResp, f.copyErr
}
func (f *fakeCreateSvc) UpdateAgent(_ context.Context, id string, req *sdk.UpdateAgentRequest) (*sdk.Agent, error) {
	f.updateCalled = true
	f.updateID = id
	f.updateReq = req
	return f.updateResp, f.updateErr
}

func TestCreate_HappyPath_MinimalRequired(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{createResp: &sdk.Agent{ID: "ag_new", Name: "Test"}}
	opts := &CreateOptions{Name: "Test", Model: "model-x"}
	err := runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc)
	require.NoError(t, err)
	require.NotNil(t, svc.createReq)
	assert.Equal(t, "Test", svc.createReq.Name)
	require.NotNil(t, svc.createReq.Config)
	assert.Equal(t, "model-x", svc.createReq.Config.ModelID)
}

func TestCreate_MissingName_FlagError(t *testing.T) {
	cmd := NewCmdCreate(nil)
	cmd.SetArgs([]string{"--model", "model-x"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	require.Error(t, err)
	// PreRunE rejects "0 args" with our flag-error sentinel; the message
	// always carries the canonical "accepts 1 arg" phrase.
	assert.Contains(t, err.Error(), "accepts 1 arg")
}

func TestCreate_MissingModel_FlagError(t *testing.T) {
	cmd := NewCmdCreate(nil)
	cmd.SetArgs([]string{"Test"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "model" not set`)
}

func TestCreate_ConfigFile_FlagsOverrideFile(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{createResp: &sdk.Agent{ID: "ag_new"}}
	opts := &CreateOptions{
		Name:           "Test",
		Model:          "model-x", // override file
		ConfigFileBody: bytes.NewBufferString(`{"agent_mode":"smart-reasoning","model_id":"model-y","temperature":0.5}`),
		ConfigFileKind: "json",
	}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.createReq.Config)
	assert.Equal(t, "smart-reasoning", svc.createReq.Config.AgentMode, "file value preserved when no flag override")
	assert.Equal(t, "model-x", svc.createReq.Config.ModelID, "flag overrides file")
	assert.InDelta(t, 0.5, svc.createReq.Config.Temperature, 0.001)
}

func TestCreate_From_CopiesThenUpdates(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{
		copyResp:   &sdk.Agent{ID: "ag_clone", Name: "Source", Config: &sdk.AgentConfig{ModelID: "model-y"}},
		updateResp: &sdk.Agent{ID: "ag_clone", Name: "Renamed"},
	}
	opts := &CreateOptions{Name: "Renamed", Model: "model-x", From: "ag_source"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, "ag_source", svc.copySrcID)
	require.True(t, svc.updateCalled, "must Update after Copy when overrides present")
	assert.Equal(t, "ag_clone", svc.updateID)
	assert.Equal(t, "Renamed", svc.updateReq.Name)
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, "model-x", svc.updateReq.Config.ModelID)
}

func TestCreate_GenerateSkeleton_NoAPICall(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	opts := &CreateOptions{GenerateSkeleton: true}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Nil(t, svc.createReq, "must not call CreateAgent")
	assert.Equal(t, "", svc.copySrcID, "must not call CopyAgent")
	assert.Contains(t, out.String(), "agent_mode:", "skeleton emitted to stdout")
}

func TestCreate_RepeatedKB_ImpliesSelectedMode(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{createResp: &sdk.Agent{ID: "ag_new"}}
	opts := &CreateOptions{
		Name:  "Test",
		Model: "model-x",
		KBs:   []string{"kb_a", "kb_b"},
		flags: createFlagSet{kbsSet: true},
	}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, []string{"kb_a", "kb_b"}, svc.createReq.Config.KnowledgeBases)
	assert.Equal(t, "selected", svc.createReq.Config.KBSelectionMode, "passing --kb implies selected mode")
}

func TestCreate_SystemPromptFile_ReaderRead(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{createResp: &sdk.Agent{ID: "ag_new"}}
	opts := &CreateOptions{
		Name:               "Test",
		Model:              "model-x",
		SystemPromptReader: strings.NewReader("You are a helpful assistant.\n"),
		flags:              createFlagSet{systemPromptSet: true},
	}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Equal(t, "You are a helpful assistant.", svc.createReq.Config.SystemPrompt, "TrimSpace removes trailing newline")
}

func TestCreate_From_PreservesSourceFieldsNotOverridden(t *testing.T) {
	// Regression: with --from X and only --temperature overridden, the
	// other 33 AgentConfig fields must round-trip from the copied agent.
	// Pre-fix, runCreate built `cfg` from a zero AgentConfig{} baseline,
	// so UpdateAgent shipped temperature=0.9 plus every other field
	// zeroed — clobbering source SystemPrompt / AgentMode / KBs.
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{
		copyResp: &sdk.Agent{ID: "ag_clone", Config: &sdk.AgentConfig{
			ModelID:        "model-y",
			SystemPrompt:   "Source prompt",
			AgentMode:      "smart-reasoning",
			Temperature:    0.5,
			KnowledgeBases: []string{"kb_src_a", "kb_src_b"},
		}},
		updateResp: &sdk.Agent{ID: "ag_clone"},
	}
	// Only --temperature overridden; other fields should round-trip.
	opts := &CreateOptions{
		Name: "Renamed", Model: "model-y", From: "ag_source",
		Temperature: 0.9,
		flags:       createFlagSet{temperatureSet: true},
	}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.updateReq)
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, "Source prompt", svc.updateReq.Config.SystemPrompt, "source SystemPrompt must round-trip")
	assert.Equal(t, "smart-reasoning", svc.updateReq.Config.AgentMode, "source AgentMode must round-trip")
	assert.Equal(t, []string{"kb_src_a", "kb_src_b"}, svc.updateReq.Config.KnowledgeBases, "source KB list must round-trip when --kb not passed")
	assert.InDelta(t, 0.9, svc.updateReq.Config.Temperature, 0.001, "Temperature overridden")
}

func TestCreate_From_KBReplacesSourceList(t *testing.T) {
	// --kb on --from REPLACES the copied agent's KB list (instead of
	// merging with it). The override semantic matches a from-scratch
	// `agent create --kb a --kb b`: whatever was on the source agent is
	// discarded for KBs the caller explicitly listed.
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{
		copyResp: &sdk.Agent{ID: "ag_clone", Config: &sdk.AgentConfig{
			ModelID:        "model-y",
			KnowledgeBases: []string{"kb_src_a", "kb_src_b"},
		}},
		updateResp: &sdk.Agent{ID: "ag_clone"},
	}
	opts := &CreateOptions{
		Name: "X", Model: "model-y", From: "ag_source",
		KBs:   []string{"kb_new"},
		flags: createFlagSet{kbsSet: true},
	}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	require.NotNil(t, svc.updateReq.Config)
	assert.Equal(t, []string{"kb_new"}, svc.updateReq.Config.KnowledgeBases, "--kb replaces source KB list")
	assert.Equal(t, "selected", svc.updateReq.Config.KBSelectionMode, "--kb on --from implies selected mode")
}

func TestCreate_Temperature_Bounds(t *testing.T) {
	for _, badT := range []float64{-0.1, 2.1, 100.0} {
		t.Run(fmt.Sprintf("t=%g", badT), func(t *testing.T) {
			cmd := NewCmdCreate(nil)
			cmd.SetArgs([]string{"Test", "--model", "model-x", "--temperature", fmt.Sprintf("%f", badT)})
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			require.Error(t, err, "expected error for --temperature %g", badT)
			assert.Contains(t, err.Error(), "0.0..2.0")
		})
	}
}

func TestCreate_CopyAgent_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{copyErr: errBadHTTP404}
	opts := &CreateOptions{Name: "X", Model: "model-x", From: "ag_missing"}
	err := runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource.not_found")
}

// errBadHTTP404 simulates the SDK's "HTTP error 404: not found" format that
// ClassifyHTTPError parses. Defined here so create_test and edit_test/delete_test
// can share it via package scope without spinning up an HTTP server.
var errBadHTTP404 = &simpleErr{msg: "HTTP error 404: not found"}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }
