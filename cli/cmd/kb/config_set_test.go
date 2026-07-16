package kb

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeInitSvc struct {
	gotKB  string
	gotCfg *sdk.KBModelConfig
	result *sdk.KBModelConfigView
	setErr error
}

func (f *fakeInitSvc) SetKBModelConfig(_ context.Context, kbID string, cfg *sdk.KBModelConfig) error {
	f.gotKB = kbID
	f.gotCfg = cfg
	return f.setErr
}

func (f *fakeInitSvc) GetInitializationConfig(_ context.Context, _ string) (*sdk.KBModelConfigView, error) {
	if f.result != nil {
		return f.result, nil
	}
	if f.gotCfg == nil {
		return &sdk.KBModelConfigView{}, nil
	}
	return &sdk.KBModelConfigView{
		RetrievalReady: f.gotCfg.EmbeddingModelID != "",
		Embedding:      sdk.ModelSlotView{Configured: f.gotCfg.EmbeddingModelID != "", ModelName: f.gotCfg.EmbeddingModelID},
		LLM:            sdk.ModelSlotView{Configured: f.gotCfg.LLMModelID != "", ModelName: f.gotCfg.LLMModelID},
	}, nil
}

func TestKBConfigSet_AppliesAndEmitsResult(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeInitSvc{}
	opts := &ConfigSetOptions{ChatModel: "model_llm", EmbeddingModel: "model_emb"}
	require.NoError(t, runConfigSet(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))

	assert.Equal(t, "kb_abc", svc.gotKB)
	require.NotNil(t, svc.gotCfg)
	assert.Equal(t, "model_llm", svc.gotCfg.LLMModelID)
	assert.Equal(t, "model_emb", svc.gotCfg.EmbeddingModelID)

	var env struct {
		OK   bool                  `json:"ok"`
		Data sdk.KBModelConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.OK)
	assert.Equal(t, "model_emb", env.Data.Embedding.ModelName)
	assert.Equal(t, "model_llm", env.Data.LLM.ModelName)
	assert.True(t, env.Data.RetrievalReady)
}

func TestKBConfigSet_RequiresBothModels(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeInitSvc{}
	// Missing both.
	err := runConfigSet(context.Background(), &ConfigSetOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_abc")
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, ce.Code)
	assert.Equal(t, "", svc.gotKB, "must not call SetKBModelConfig when flags are missing")

	// Missing just embedding.
	err = runConfigSet(context.Background(), &ConfigSetOptions{ChatModel: "m"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_abc")
	require.ErrorAs(t, err, &ce)
	assert.Contains(t, ce.Message, "--embedding-model")
}

func TestKBConfigSet_WriteSucceedsReadbackFails(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc2 := &readbackErrSvc{fakeInitSvc: &fakeInitSvc{}}
	opts := &ConfigSetOptions{ChatModel: "model_llm", EmbeddingModel: "model_emb"}
	require.NoError(t, runConfigSet(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc2, "kb_abc"))
	var env struct {
		Data sdk.KBModelConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.Equal(t, "model_emb", env.Data.Embedding.ModelName, "falls back to applied config when read-back fails")
}

type readbackErrSvc struct{ *fakeInitSvc }

func (s *readbackErrSvc) GetInitializationConfig(_ context.Context, _ string) (*sdk.KBModelConfigView, error) {
	return nil, errors.New("read-back boom")
}

// withRootKB wraps a kb subcommand under a synthetic root with global flags,
// keyed off the sub's own name (unlike withRootHarnessKB which is hardcoded to
// `update`).
func withRootKB(sub *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	kb := &cobra.Command{Use: "kb"}
	kb.AddCommand(sub)
	root.AddCommand(kb)
	root.SetArgs(append([]string{"kb", sub.Name()}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

func TestKBConfigSet_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t)
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return nil, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	// Drive `kb config set` (the config parent routes to its `set` subcommand).
	root := withRootKB(NewCmdConfig(f), "set", "kb_abc", "--chat-model", "model_llm", "--embedding-model", "model_emb", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.Contains(t, ce.RetryArgv, "-y")
	assert.Contains(t, ce.RetryArgv, "model_emb")
	assert.Contains(t, ce.RetryArgv, "set", "retry_argv should target `kb config set`")
}
