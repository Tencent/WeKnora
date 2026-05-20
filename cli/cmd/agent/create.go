package agentcmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// CreateService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it via duck typing.
type CreateService interface {
	CreateAgent(ctx context.Context, req *sdk.CreateAgentRequest) (*sdk.Agent, error)
	CopyAgent(ctx context.Context, id string) (*sdk.Agent, error)
	UpdateAgent(ctx context.Context, id string, req *sdk.UpdateAgentRequest) (*sdk.Agent, error)
}

// CreateOptions captures flag state. SystemPromptReader and ConfigFileBody
// are populated from --system-prompt-file and --config-file respectively
// (or stdin when the value is "-"). The embedded flags struct tracks "was
// set" bits so MergeAgentConfig can distinguish "user passed --foo zero"
// from "user did not pass --foo".
type CreateOptions struct {
	Name               string
	Model              string
	Description        string
	SystemPrompt       string
	SystemPromptReader io.Reader
	AgentMode          string
	KBs                []string
	KBSelectionMode    string
	RerankModel        string
	Temperature        float64
	From               string
	ConfigFileBody     io.Reader
	ConfigFileKind     string // "yaml" or "json"
	GenerateSkeleton   bool

	flags createFlagSet // populated in PreRunE for *Set bits
}

// createFlagSet records which hot-path flags the user explicitly passed so
// MergeAgentConfig knows which fields to overlay onto the base config.
type createFlagSet struct {
	agentModeSet       bool
	systemPromptSet    bool
	rerankModelSet     bool
	temperatureSet     bool
	kbSelectionModeSet bool
	kbsSet             bool
}

const agentCreateExample = `  weknora agent create "Support Bot" --model <model-id>
  weknora agent create "Code Reviewer" --model <model-id> --system-prompt-file ./prompt.md --kb kb_eng --kb kb_arch
  weknora agent create "From Template" --model <model-id> --from ag_existing
  weknora agent create --generate-skeleton > my-agent.yaml
  weknora agent create "Tuned" --model <model-id> --config-file ./my-agent.yaml`

const agentCreateLong = `Create a new custom agent.

--model is required (an agent without a model cannot invoke). The 7
optional hot-path flags cover the most frequently set AgentConfig fields;
for the remaining 27 use --config-file with a YAML or JSON document
matching the AgentConfig schema (run --generate-skeleton to get a
ready-to-edit template).

Precedence when both a config file and hot-path flags are supplied:
  hot-path flag > config-file value > server default

--from <id> copies an existing agent (SDK CopyAgent) then applies any
hot-path overrides via UpdateAgent. --kb on --from REPLACES the copied
agent's KB list (not merge) — matches surgical-flag semantics.

AI agents: writes a new resource server-side. Failure surfaces as a
typed code on stderr: input.invalid_argument (bad flags, bad file, or
bad model), resource.not_found (--from <missing>), auth.unauthenticated.`

// NewCmdCreate builds `weknora agent create <name> --model <id>`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{}
	var systemPromptFile, configFile string

	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a new custom agent",
		Long:    agentCreateLong,
		Example: agentCreateExample,
		// Allow 0 args for --generate-skeleton; PreRunE enforces the real
		// arity rule once it knows whether skeleton mode is active.
		Args: cobra.MaximumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.GenerateSkeleton {
				return nil
			}
			if len(args) != 1 {
				return cmdutil.NewFlagError(fmt.Errorf("accepts 1 arg, received %d", len(args)))
			}
			opts.Name = args[0]
			if opts.Model == "" {
				return cmdutil.NewFlagError(fmt.Errorf(`required flag(s) "model" not set`))
			}
			opts.flags.agentModeSet = cmd.Flags().Changed("agent-mode")
			opts.flags.systemPromptSet = cmd.Flags().Changed("system-prompt") || cmd.Flags().Changed("system-prompt-file")
			opts.flags.rerankModelSet = cmd.Flags().Changed("rerank-model")
			opts.flags.temperatureSet = cmd.Flags().Changed("temperature")
			opts.flags.kbSelectionModeSet = cmd.Flags().Changed("kb-selection-mode")
			opts.flags.kbsSet = cmd.Flags().Changed("kb")

			// --temperature is bounded 0.0..2.0. Reject out-of-range
			// early with a typed input.invalid_argument so users don't
			// burn a roundtrip on a value the server would also reject.
			if opts.flags.temperatureSet && (opts.Temperature < 0.0 || opts.Temperature > 2.0) {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
					fmt.Sprintf("--temperature must be in 0.0..2.0, got %g", opts.Temperature))
			}

			if systemPromptFile != "" {
				r, err := cmdutil.OpenInput(systemPromptFile)
				if err != nil {
					return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("--system-prompt-file: %v", err))
				}
				opts.SystemPromptReader = r
			}
			if configFile != "" {
				r, kind, err := openConfigFile(configFile)
				if err != nil {
					return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("--config-file: %v", err))
				}
				opts.ConfigFileBody = r
				opts.ConfigFileKind = kind
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(cmd)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runCreate(cmd.Context(), opts, fopts, cli)
		},
	}

	// Required (enforced in PreRunE rather than cmd.MarkFlagRequired so
	// --generate-skeleton can bypass it — cobra's MarkFlagRequired runs
	// before PreRunE and would otherwise block the skeleton path).
	cmd.Flags().StringVar(&opts.Model, "model", "", "LLM model id (required, except with --generate-skeleton)")

	// Hot-path (8 flag names, 7 distinct config fields)
	cmd.Flags().StringVar(&opts.Description, "description", "", "Agent description")
	cmd.Flags().StringVar(&opts.SystemPrompt, "system-prompt", "", "System prompt text (mutex with --system-prompt-file)")
	cmd.Flags().StringVar(&systemPromptFile, "system-prompt-file", "", "Read system prompt from FILE, or '-' for stdin")
	cmd.MarkFlagsMutuallyExclusive("system-prompt", "system-prompt-file")
	cmd.Flags().StringVar(&opts.AgentMode, "agent-mode", "", "Agent operating mode: quick-answer | smart-reasoning")
	cmd.Flags().StringSliceVar(&opts.KBs, "kb", nil, "Attach knowledge base id (repeatable)")
	cmd.Flags().StringVar(&opts.KBSelectionMode, "kb-selection-mode", "", "KB selection mode: all | selected | none")
	cmd.Flags().StringVar(&opts.RerankModel, "rerank-model", "", "Rerank model id")
	cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0.0, "Generation temperature (0.0..2.0)")

	// Power-user / utility
	cmd.Flags().StringVar(&opts.From, "from", "", "Copy from existing agent id (then apply other flags)")
	cmd.Flags().StringVar(&configFile, "config-file", "", "Full AgentConfig YAML or JSON (use '-' for stdin)")
	cmd.Flags().BoolVar(&opts.GenerateSkeleton, "generate-skeleton", false, "Emit blank AgentConfig YAML to stdout and exit")

	cmdutil.AddFormatFlag(cmd, agentViewFields...)
	return cmd
}

func runCreate(ctx context.Context, opts *CreateOptions, fopts *cmdutil.FormatOptions, svc CreateService) error {
	if opts.GenerateSkeleton {
		return cmdutil.GenerateAgentSkeleton(iostreams.IO.Out)
	}

	// 1. Build base AgentConfig from --config-file (if any), else zero.
	// (For --from, the copied agent's existing config becomes the base
	// instead — set after CopyAgent below, before MergeAgentConfig.)
	var base sdk.AgentConfig
	if opts.ConfigFileBody != nil {
		parsed, err := cmdutil.LoadAgentConfig(opts.ConfigFileBody, opts.ConfigFileKind)
		if err != nil {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, err.Error())
		}
		base = *parsed
	}

	// 2. Resolve system prompt (file/stdin > flag string)
	if opts.SystemPromptReader != nil {
		body, err := io.ReadAll(opts.SystemPromptReader)
		if err != nil {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("--system-prompt-file read: %v", err))
		}
		opts.SystemPrompt = strings.TrimSpace(string(body))
	}

	// 3. Either copy-then-update, or create from scratch. For --from we
	// must seed `base` from the copied agent's existing config FIRST so
	// MergeAgentConfig preserves source fields the user did not override
	// (e.g. SystemPrompt, AgentMode, KB list). Without this seeding the
	// surgical overrides would ship UpdateAgent with the other 33 fields
	// zeroed, clobbering source state.
	if opts.From != "" {
		copied, err := svc.CopyAgent(ctx, opts.From)
		if err != nil {
			return cmdutil.WrapHTTP(err, "copy agent %s", opts.From)
		}
		if copied.Config != nil {
			base = *copied.Config
		}

		// --kb on --from REPLACES the copied KB list; when --kb
		// is not set, KnowledgeBasesSet stays false inside applyCreateOverrides
		// and the copy's KB list passes through unchanged.
		cfg := applyCreateOverrides(&base, opts)

		// Apply overrides on top of copied state via UpdateAgent (no
		// server-side template-parameters route, so we do two roundtrips).
		updateReq := &sdk.UpdateAgentRequest{
			Name:        opts.Name,
			Description: opts.Description,
			Config:      cfg,
		}
		updated, err := svc.UpdateAgent(ctx, copied.ID, updateReq)
		if err != nil {
			return cmdutil.WrapHTTP(err, "update copied agent %s", copied.ID)
		}
		return emitAgent(fopts, updated)
	}

	// 4. Plain create path: apply hot-path flag overrides onto base
	// (zero-valued unless --config-file supplied content).
	cfg := applyCreateOverrides(&base, opts)

	req := &sdk.CreateAgentRequest{
		Name:        opts.Name,
		Description: opts.Description,
		Config:      cfg,
	}
	created, err := svc.CreateAgent(ctx, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "create agent")
	}
	return emitAgent(fopts, created)
}

// applyCreateOverrides merges hot-path flag overrides into the base config,
// then applies the "--kb implies --kb-selection-mode=selected" fallback.
// Shared by the --from path (base=copied agent's config) and the plain
// create path (base=zero or --config-file). Keeping both paths on one
// helper prevents silent divergence when future flags are added.
func applyCreateOverrides(base *sdk.AgentConfig, opts *CreateOptions) *sdk.AgentConfig {
	overrides := cmdutil.AgentConfigFlags{
		AgentMode: opts.AgentMode, AgentModeSet: opts.flags.agentModeSet,
		SystemPrompt: opts.SystemPrompt, SystemPromptSet: opts.flags.systemPromptSet,
		ModelID: opts.Model, ModelIDSet: true, // --model is required
		RerankModelID: opts.RerankModel, RerankModelIDSet: opts.flags.rerankModelSet,
		Temperature: opts.Temperature, TemperatureSet: opts.flags.temperatureSet,
		KBSelectionMode: opts.KBSelectionMode, KBSelectionModeSet: opts.flags.kbSelectionModeSet,
		KnowledgeBases: opts.KBs, KnowledgeBasesSet: opts.flags.kbsSet,
	}
	cfg := cmdutil.MergeAgentConfig(base, overrides)

	// --kb without explicit --kb-selection-mode implies "selected".
	if opts.flags.kbsSet && !opts.flags.kbSelectionModeSet && cfg.KBSelectionMode == "" {
		cfg.KBSelectionMode = "selected"
	}
	return cfg
}

// openConfigFile returns a Reader, the detected kind ("yaml"/"json"), or
// an error. Format is inferred from file extension; "-" defaults to YAML.
func openConfigFile(path string) (io.Reader, string, error) {
	r, err := cmdutil.OpenInput(path)
	if err != nil {
		return nil, "", err
	}
	kind := "yaml"
	if strings.EqualFold(filepath.Ext(path), ".json") {
		kind = "json"
	}
	return r, kind, nil
}

// emitAgent writes the Agent to stdout per the v0.4 wire contract (bare
// SDK shape for --format json, human KV otherwise). Shared by create and edit;
// defined here for proximity to the create flow.
func emitAgent(fopts *cmdutil.FormatOptions, ag *sdk.Agent) error {
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, ag)
	}
	renderAgent(iostreams.IO.Out, ag)
	return nil
}
