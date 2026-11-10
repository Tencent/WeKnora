package modelcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// modelCreateFields enumerates the fields surfaced for `--format json` discovery
// on `model create`. The result is the created Model.
var modelCreateFields = []string{
	"id", "name", "display_name", "type", "source",
	"description", "is_default", "parameters", "created_at",
}

// createSourceValues is the restricted --source set for `model create`. The
// server's CreateModel only special-cases "remote" (registered active, routed
// to a provider API); every other source falls into the local Ollama download
// path. So create offers exactly the two working modes — the provider identity
// for a remote model goes in --provider, not --source. (`model list --source`
// still accepts the broad modelSourceValues for filtering pre-existing records.)
var createSourceValues = []string{string(sdk.ModelSourceLocal), string(sdk.ModelSourceRemote)}

// canonicalModelType maps the server's frontend term "chat" to the KnowledgeQA
// enum — that is the server's own /models/providers vocabulary (see its
// frontendToModelType map). The other frontend terms (embedding / rerank /
// vllm / asr) already match the enum case-insensitively, so no alias is needed.
// Returns the input unchanged when it isn't an alias.
func canonicalModelType(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), "chat") {
		return string(sdk.ModelTypeKnowledgeQA)
	}
	return t
}

type CreateOptions struct {
	Name        string
	DisplayName string
	Description string
	Type        string
	Source      string
	Provider    string
	BaseURL     string
	APIKeyStdin bool
	Dimension   int
	Default     bool
	Params      []string // repeatable key=value → top-level Parameters entries
	DryRun      bool
	StdinReader io.Reader // overridden by tests
}

// CreateService is the narrow SDK surface this command depends on.
// ListModelProviders supplies the authoritative provider catalog used to
// validate --provider and default --base-url for remote models.
type CreateService interface {
	CreateModel(ctx context.Context, req *sdk.CreateModelRequest) (*sdk.Model, error)
	ListModelProviders(ctx context.Context, modelType string) ([]sdk.ModelProvider, error)
}

// frontendModelType maps the create enum to the server's /models/providers
// "model_type" query vocabulary (KnowledgeQA→chat, VLLM→vllm; others lowercase).
func frontendModelType(t string) string {
	switch t {
	case string(sdk.ModelTypeKnowledgeQA):
		return "chat"
	case string(sdk.ModelTypeVLLM):
		return "vllm"
	default:
		return strings.ToLower(t)
	}
}

// NewCmdCreate builds `weknora model create <name>`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Register a model (embedding / rerank / chat / VLLM / ASR)",
		Long: `Register a model on the server so it can back a knowledge base's embedding /
summary config (see 'weknora kb config set') or an agent (--model).

<name> is the model name as the provider knows it (e.g. "nomic-embed-text",
"gpt-4o", "qwen2"). --type and --source are required.

Two modes:

  Local (Ollama):  --source local
                   The server pulls <name> from Ollama (async download).
                   --base-url points at the Ollama endpoint when not default.

  Remote (API):    --source remote --provider <name> [--api-key-stdin] [--base-url <url>]
                   Registered active and routed to the provider's API.
                   --provider is required and is validated against the server's
                   live provider catalog (weknora api /api/v1/models/providers);
                   --base-url defaults to that provider's URL for the type when
                   omitted.

--type accepts the server's term "chat" for KnowledgeQA (embedding/rerank/vllm/
asr match the enum directly). Embedding models take --dimension. Pipe the
provider key via --api-key-stdin so it never lands in argv/history. Anything
else goes through repeatable --param key=value.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Name = args[0]
			// Validate + normalize enums (case-insensitive) before the dry-run
			// gate so --dry-run rejects identically to the live path. Mirrors
			// `model list`, which accepts the same flags case-insensitively and
			// fails an unknown value as input.invalid_argument (exit 5).
			canonType, err := cmdutil.ValidateEnum("type", canonicalModelType(opts.Type), modelTypeValues)
			if err != nil {
				return err
			}
			opts.Type = canonType
			canonSource, err := cmdutil.ValidateEnum("source", opts.Source, createSourceValues)
			if err != nil {
				return err
			}
			opts.Source = canonSource
			// Mode-specific guardrails: a remote model needs a provider to route
			// its API calls; a local (Ollama) model has no provider concept.
			if opts.Source == "remote" && strings.TrimSpace(opts.Provider) == "" {
				return cmdutil.NewError(cmdutil.CodeInputMissingFlag,
					"--source remote requires --provider (e.g. openai, aliyun, deepseek)").
					WithHint("for a local Ollama model use --source local (no --provider)")
			}
			if opts.Source == "local" && strings.TrimSpace(opts.Provider) != "" {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
					"--provider applies to --source remote; a local model is pulled from Ollama by name").
					WithHint("drop --provider, or switch to --source remote")
			}
			params, err := parseParams(opts.Params)
			if err != nil {
				return err
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "model.create",
				// Never echo the API key into the plan (it is read from stdin
				// precisely so it never lands in argv / history / dry-run output).
				Args: map[string]any{"name": opts.Name, "type": opts.Type, "source": opts.Source, "provider": opts.Provider},
			}); handled {
				return err
			}
			if opts.StdinReader == nil {
				opts.StdinReader = iostreams.IO.In
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runCreate(c.Context(), opts, fopts, cli, params)
		},
	}
	cmd.Flags().StringVar(&opts.Type, "type", "", "Model type: "+strings.Join(modelTypeValues, " | ")+" (required; \"chat\" is accepted for KnowledgeQA)")
	cmd.Flags().StringVar(&opts.Source, "source", "", "Where the model runs: "+strings.Join(createSourceValues, " | ")+" (required; local=Ollama, remote=provider API)")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Remote provider id, required+validated with --source remote (see `weknora api /api/v1/models/providers`)")
	cmd.Flags().StringVar(&opts.DisplayName, "display-name", "", "Human-friendly name (optional)")
	cmd.Flags().StringVar(&opts.Description, "description", "", "Description (optional)")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "Model API base URL (e.g. http://localhost:11434 for Ollama)")
	cmd.Flags().BoolVar(&opts.APIKeyStdin, "api-key-stdin", false, "Read the provider API key from stdin (kept out of argv / history)")
	cmd.Flags().IntVar(&opts.Dimension, "dimension", 0, "Embedding dimension (Embedding models only)")
	cmd.Flags().BoolVar(&opts.Default, "default", false, "Mark this the default model for its type")
	cmd.Flags().StringArrayVar(&opts.Params, "param", nil, "Extra provider parameter as key=value, repeatable (value parsed as JSON: true/42/text)")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("source")
	cmdutil.AddFormatFlag(cmd, modelCreateFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "register a model (embedding/rerank/chat/VLLM/ASR) so a KB or agent can use it; capture .data.id to pass to `weknora kb config set` / `agent create --model`.",
		RequiredFlags: []string{"<name> (positional)", "--type", "--source (local|remote)", "--provider (when --source remote)"},
		Examples: []string{
			`weknora model create nomic-embed-text --type Embedding --source local --dimension 768   # Ollama (server pulls it)`,
			`printf '%s' "$OPENAI_KEY" | weknora model create text-embedding-3-small --type Embedding --source remote --provider openai --dimension 1536 --api-key-stdin`,
		},
		Output: "envelope.data is the created Model object with id, name, type, source, parameters",
		Warnings: []string{
			"Two modes: --source local (Ollama pulls <name>, async) vs --source remote --provider <id> (provider API). A provider name is NOT a --source value.",
			"Pass the API key via --api-key-stdin (piped), never as a flag — flag values leak into ps/history.",
			"A local model starts in a 'downloading' state and is unusable until the pull finishes; the embedding/chat call fails until then.",
		},
	})
	return cmd
}

// parseParams turns repeated key=value flags into a map. Each value is parsed
// as JSON so true/false, numbers, and objects keep their type (the server's
// ModelParameters has typed fields like supports_vision bool); a value that
// isn't valid JSON is kept as a plain string. Returns a typed flag error on a
// malformed entry so the failure is exit 2, not a server 400.
func parseParams(kvs []string) (map[string]any, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, cmdutil.NewFlagError(fmt.Errorf("invalid --param %q: expected key=value", kv))
		}
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			out[k] = parsed // true/false, numbers, JSON objects/arrays
		} else {
			out[k] = v // plain string (the common case)
		}
	}
	return out, nil
}

func runCreate(ctx context.Context, opts *CreateOptions, fopts *cmdutil.FormatOptions, svc CreateService, params map[string]any) error {
	// Remote models: validate --provider against the server's live provider
	// catalog for this model type, and default --base-url from it when omitted.
	// Uses the authoritative /models/providers data (via the SDK) instead of a
	// hardcoded list, so the CLI never drifts from the server.
	if opts.Source == string(sdk.ModelSourceRemote) {
		if err := resolveRemoteProvider(ctx, svc, opts); err != nil {
			return err
		}
	}
	parameters := sdk.ModelParameters{}
	for k, v := range params {
		parameters[k] = v
	}
	if opts.Provider != "" {
		parameters["provider"] = opts.Provider
	}
	if opts.BaseURL != "" {
		parameters["base_url"] = opts.BaseURL
	}
	if opts.Dimension > 0 {
		parameters["embedding_parameters"] = map[string]any{"dimension": opts.Dimension}
	}
	if opts.APIKeyStdin {
		key, err := readStdinTrimmed(opts.StdinReader)
		if err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read API key from stdin")
		}
		if key == "" {
			return cmdutil.NewError(cmdutil.CodeInputMissingFlag, "--api-key-stdin requires the key piped to stdin")
		}
		parameters["api_key"] = key
	}

	req := &sdk.CreateModelRequest{
		Name:        opts.Name,
		DisplayName: opts.DisplayName,
		Type:        sdk.ModelType(opts.Type),
		Source:      sdk.ModelSource(opts.Source),
		Description: opts.Description,
		Parameters:  parameters,
		IsDefault:   opts.Default,
	}
	created, err := svc.CreateModel(ctx, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "create model")
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, created, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Created model %q (id: %s, type: %s)\n", created.Name, created.ID, created.Type)
	return nil
}

// resolveRemoteProvider validates opts.Provider against the server's provider
// catalog for the model's type and, when --base-url was omitted, defaults it
// from the provider's catalog entry. Canonicalizes the provider's casing.
func resolveRemoteProvider(ctx context.Context, svc CreateService, opts *CreateOptions) error {
	ft := frontendModelType(opts.Type)
	providers, err := svc.ListModelProviders(ctx, ft)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list model providers")
	}
	for i := range providers {
		if strings.EqualFold(providers[i].Value, opts.Provider) {
			opts.Provider = providers[i].Value // canonicalize casing
			if opts.BaseURL == "" {
				opts.BaseURL = providers[i].DefaultURLs[ft]
			}
			return nil
		}
	}
	vals := make([]string, len(providers))
	for i, p := range providers {
		vals[i] = p.Value
	}
	return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
		fmt.Sprintf("unknown --provider %q for %s models", opts.Provider, opts.Type)).
		WithHint("supported providers: " + strings.Join(vals, ", "))
}

// readStdinTrimmed reads all of r and returns it whitespace-trimmed.
func readStdinTrimmed(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// compile-time check: the production SDK client implements CreateService.
var _ CreateService = (*sdk.Client)(nil)
