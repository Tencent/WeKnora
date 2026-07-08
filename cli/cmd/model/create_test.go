package modelcmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeCreateSvc struct {
	got       *sdk.CreateModelRequest
	resp      *sdk.Model
	err       error
	providers []sdk.ModelProvider // returned by ListModelProviders; nil → default catalog
}

func (f *fakeCreateSvc) CreateModel(_ context.Context, req *sdk.CreateModelRequest) (*sdk.Model, error) {
	f.got = req
	if f.resp == nil {
		f.resp = &sdk.Model{ID: "model_new", Name: req.Name, Type: req.Type, Source: req.Source}
	}
	return f.resp, f.err
}

func (f *fakeCreateSvc) ListModelProviders(_ context.Context, _ string) ([]sdk.ModelProvider, error) {
	if f.providers != nil {
		return f.providers, nil
	}
	return []sdk.ModelProvider{
		{Value: "openai", ModelTypes: []string{"chat", "embedding"},
			DefaultURLs: map[string]string{"chat": "https://api.openai.com/v1", "embedding": "https://api.openai.com/v1"}},
		{Value: "aliyun", ModelTypes: []string{"chat", "embedding", "rerank"},
			DefaultURLs: map[string]string{"embedding": "https://dashscope.example/v1"}},
	}, nil
}

func TestModelCreate_BuildsRequest(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	opts := &CreateOptions{
		Name: "nomic-embed-text", Type: "Embedding", Source: "local",
		BaseURL: "http://localhost:11434", Dimension: 768, Default: true,
	}
	require.NoError(t, runCreate(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, map[string]any{"provider": "ollama"}))

	require.NotNil(t, svc.got)
	assert.Equal(t, "nomic-embed-text", svc.got.Name)
	assert.Equal(t, sdk.ModelTypeEmbedding, svc.got.Type)
	assert.Equal(t, sdk.ModelSource("local"), svc.got.Source)
	assert.True(t, svc.got.IsDefault)
	assert.Equal(t, "http://localhost:11434", svc.got.Parameters["base_url"])
	assert.Equal(t, "ollama", svc.got.Parameters["provider"])
	emb, ok := svc.got.Parameters["embedding_parameters"].(map[string]any)
	require.True(t, ok, "embedding_parameters should be a nested map")
	assert.Equal(t, 768, emb["dimension"])

	var env struct {
		OK   bool      `json:"ok"`
		Data sdk.Model `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.OK)
	assert.Equal(t, "model_new", env.Data.ID)
}

func TestModelCreate_APIKeyFromStdin(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	opts := &CreateOptions{
		Name: "text-embedding-3-small", Type: "Embedding", Source: "remote", Provider: "openai",
		APIKeyStdin: true, StdinReader: strings.NewReader("  sk-secret\n"),
	}
	require.NoError(t, runCreate(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, nil))
	assert.Equal(t, "sk-secret", svc.got.Parameters["api_key"], "key must be trimmed and set from stdin")
	assert.Equal(t, "openai", svc.got.Parameters["provider"], "--provider maps into parameters.provider")
}

// TestModelCreate_RemoteProviderValidatedAndBaseURLDefaulted: for a remote
// model the provider is validated against the server's catalog and --base-url
// is defaulted from it (not hardcoded in the CLI).
func TestModelCreate_RemoteProviderValidatedAndBaseURLDefaulted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	// Known provider, no --base-url → defaulted from the catalog entry.
	svc := &fakeCreateSvc{}
	require.NoError(t, runCreate(context.Background(),
		&CreateOptions{Name: "text-embedding-3-small", Type: "Embedding", Source: "remote", Provider: "openai"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, nil))
	assert.Equal(t, "openai", svc.got.Parameters["provider"])
	assert.Equal(t, "https://api.openai.com/v1", svc.got.Parameters["base_url"], "base_url defaulted from provider catalog")

	// Explicit --base-url is respected (not overridden by the catalog default).
	svc2 := &fakeCreateSvc{}
	require.NoError(t, runCreate(context.Background(),
		&CreateOptions{Name: "m", Type: "Embedding", Source: "remote", Provider: "openai", BaseURL: "https://proxy.local/v1"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc2, nil))
	assert.Equal(t, "https://proxy.local/v1", svc2.got.Parameters["base_url"])

	// Unknown provider → input.invalid_argument, no model created.
	svc3 := &fakeCreateSvc{}
	err := runCreate(context.Background(),
		&CreateOptions{Name: "x", Type: "Embedding", Source: "remote", Provider: "bogus-provider"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc3, nil)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	assert.Nil(t, svc3.got, "must not create a model when --provider is unknown")
}

func TestModelCreate_APIKeyStdinEmpty(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	opts := &CreateOptions{Name: "m", Type: "Embedding", Source: "openai", APIKeyStdin: true, StdinReader: strings.NewReader("")}
	err := runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, nil)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, ce.Code)
	assert.Nil(t, svc.got, "must not call CreateModel when stdin key is empty")
}

func TestParseParams(t *testing.T) {
	// Bare words aren't valid JSON → kept as strings (the common case).
	m, err := parseParams([]string{"provider=openai", "interface_type=chat"})
	require.NoError(t, err)
	assert.Equal(t, "openai", m["provider"])
	assert.Equal(t, "chat", m["interface_type"])

	// JSON-typed values keep their type so the server's typed ModelParameters
	// fields (e.g. supports_vision bool) bind correctly.
	typed, err := parseParams([]string{"supports_vision=true", "n=42", "name=plain"})
	require.NoError(t, err)
	assert.Equal(t, true, typed["supports_vision"])
	assert.Equal(t, float64(42), typed["n"])
	assert.Equal(t, "plain", typed["name"])

	_, err = parseParams([]string{"noequals"})
	require.Error(t, err)
	var fe *cmdutil.FlagError
	assert.ErrorAs(t, err, &fe)
}

// withRootHarnessModel wraps a model subcommand under a synthetic root with the
// global persistent flags so flag/enum validation and gating route correctly.
func withRootHarnessModel(sub *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	model := &cobra.Command{Use: "model"}
	model.AddCommand(sub)
	root.AddCommand(model)
	root.SetArgs(append([]string{"model", sub.Name()}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

func TestModelCreate_InvalidTypeRejected(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdCreate(modelDryRunFactory(t)),
		"my-model", "--type", "Bogus", "--source", "local", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	// An unknown enum VALUE is input.invalid_argument (exit 5) — same as
	// `model list`, not a cobra parse error (exit 2).
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	assert.Equal(t, 5, cmdutil.ExitCode(err))
}

// TestModelCreate_NormalizesTypeAliasAndSourceCase: --type accepts friendly
// aliases (chat → KnowledgeQA) and --source is case-insensitive, both
// normalized to canonical form in the plan.
func TestModelCreate_NormalizesTypeAliasAndSourceCase(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdCreate(modelDryRunFactory(t)),
		"m", "--type", "chat", "--source", "REMOTE", "--provider", "openai", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute())
	var env struct {
		Meta struct {
			Plan map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	args, ok := env.Meta.Plan["args"].(map[string]any)
	require.True(t, ok, "plan.args should be present; got %v", env.Meta.Plan)
	assert.Equal(t, "KnowledgeQA", args["type"], "chat alias normalized to KnowledgeQA")
	assert.Equal(t, "remote", args["source"], "source normalized to lowercase canonical")
}

// TestModelCreate_RemoteRequiresProvider: --source remote without --provider is
// rejected (the server can't route a remote model without a provider).
func TestModelCreate_RemoteRequiresProvider(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdCreate(modelDryRunFactory(t)),
		"gpt-4o", "--type", "KnowledgeQA", "--source", "remote", "--format", "json")
	err := root.Execute()
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, ce.Code)
}

// TestModelCreate_LocalRejectsProvider: --provider with --source local is a
// contradiction (a local Ollama model has no provider).
func TestModelCreate_LocalRejectsProvider(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdCreate(modelDryRunFactory(t)),
		"qwen2", "--type", "KnowledgeQA", "--source", "local", "--provider", "openai", "--format", "json")
	err := root.Execute()
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
}

// TestModelCreate_RejectsProviderNameAsSource: a provider name is not a valid
// --source (only local/remote) — guards the misleading-source regression.
func TestModelCreate_RejectsProviderNameAsSource(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessModel(NewCmdCreate(modelDryRunFactory(t)),
		"x", "--type", "Embedding", "--source", "openai", "--format", "json")
	err := root.Execute()
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
}
