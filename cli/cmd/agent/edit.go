package agentcmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// EditService is the narrow SDK surface this command depends on. The fetch
// half (GetAgent) is mandatory because UpdateAgent is a full PUT — without
// the pre-fetch baseline, any field not passed as a flag would silently
// clobber to the zero value.
type EditService interface {
	GetAgent(ctx context.Context, id string) (*sdk.Agent, error)
	UpdateAgent(ctx context.Context, id string, req *sdk.UpdateAgentRequest) (*sdk.Agent, error)
}

// EditOptions captures the surgical flag state. Both string fields and
// reader-based file inputs are tracked alongside per-flag *Set bits in
// editFlagSet so empty strings are distinguishable from "unset".
type EditOptions struct {
	AgentID            string
	Name               string
	Description        string
	Model              string
	SystemPrompt       string
	SystemPromptReader io.Reader
	AgentMode          string
	RerankModel        string
	Temperature        float64
	AddKBs             []string
	RemoveKBs          []string
	KBSelectionMode    string
	ConfigFileBody     io.Reader
	ConfigFileKind     string // "yaml" or "json"

	flags editFlagSet
}

// editFlagSet tracks which surgical flags the user passed. Empty-string
// values are valid (clear semantics) so cmd.Flags().Changed() is the only
// reliable signal of "user supplied this flag."
type editFlagSet struct {
	nameSet            bool
	descriptionSet     bool
	modelSet           bool
	systemPromptSet    bool
	agentModeSet       bool
	rerankModelSet     bool
	temperatureSet     bool
	addKBsSet          bool
	removeKBsSet       bool
	kbSelectionModeSet bool
	configFileSet      bool
}

const agentEditLong = `Edit a custom agent's fields surgically.

At least one update flag is required; flags you omit preserve the current
server-side value via fetch-then-update. Pass --description "" to clear
the description (empty string is a valid value, not "unset").

KB list operations are list-shaped: --add-kb and --remove-kb are
idempotent (re-adding an already-attached KB is silent success; removing
an unattached KB is silent success). Passing the same id to both flags
nets out to no-op and prints a stderr warning.

--config-file fully replaces the AgentConfig baseline (the same shape
GenerateAgentSkeleton emits). Surgical flags then apply on top of that
replaced baseline. To partially update one or two fields without
touching the rest, use surgical flags alone — that path L-2 fetches
current state and only mutates what's set. Precedence within a single
invocation:

  surgical flag > config-file value > zero value

AI agents: writes to a server resource. Failure surfaces as a typed
code on stderr: resource.not_found (agent id or KB id), auth.forbidden,
input.invalid_argument (no flags passed, bad file).`

const agentEditExample = `  weknora agent edit ag_abc --name "Renamed"
  weknora agent edit ag_abc --description ""                # clear description
  weknora agent edit ag_abc --add-kb kb_new --remove-kb kb_old
  weknora agent edit ag_abc --system-prompt-file ./prompt.md
  weknora agent edit ag_abc --config-file ./tuned.yaml --temperature 0.9`

// NewCmdEdit builds `weknora agent edit <agent-id>`.
func NewCmdEdit(f *cmdutil.Factory) *cobra.Command {
	opts := &EditOptions{}
	var systemPromptFile, configFile string

	cmd := &cobra.Command{
		Use:     "edit <agent-id>",
		Short:   "Edit a custom agent's configuration",
		Long:    agentEditLong,
		Example: agentEditExample,
		Args:    cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			opts.AgentID = args[0]
			opts.flags.nameSet = cmd.Flags().Changed("name")
			opts.flags.descriptionSet = cmd.Flags().Changed("description")
			opts.flags.modelSet = cmd.Flags().Changed("model")
			opts.flags.systemPromptSet = cmd.Flags().Changed("system-prompt") || cmd.Flags().Changed("system-prompt-file")
			opts.flags.agentModeSet = cmd.Flags().Changed("agent-mode")
			opts.flags.rerankModelSet = cmd.Flags().Changed("rerank-model")
			opts.flags.temperatureSet = cmd.Flags().Changed("temperature")
			opts.flags.addKBsSet = cmd.Flags().Changed("add-kb")
			opts.flags.removeKBsSet = cmd.Flags().Changed("remove-kb")
			opts.flags.kbSelectionModeSet = cmd.Flags().Changed("kb-selection-mode")
			opts.flags.configFileSet = cmd.Flags().Changed("config-file")

			// --temperature is bounded 0.0..2.0. Reject out-of-range
			// early with a typed input.invalid_argument.
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
			jopts, err := cmdutil.CheckJSONFlags(cmd)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runEdit(cmd.Context(), opts, jopts, cli)
		},
	}

	// Surgical flags
	cmd.Flags().StringVar(&opts.Name, "name", "", "New agent name")
	cmd.Flags().StringVar(&opts.Description, "description", "", `New description (use "" to clear)`)
	cmd.Flags().StringVar(&opts.Model, "model", "", "LLM model id")
	cmd.Flags().StringVar(&opts.SystemPrompt, "system-prompt", "", "System prompt text (mutex with --system-prompt-file)")
	cmd.Flags().StringVar(&systemPromptFile, "system-prompt-file", "", "Read system prompt from FILE, or '-' for stdin")
	cmd.MarkFlagsMutuallyExclusive("system-prompt", "system-prompt-file")
	cmd.Flags().StringVar(&opts.AgentMode, "agent-mode", "", "Agent operating mode: quick-answer | smart-reasoning")
	cmd.Flags().StringVar(&opts.RerankModel, "rerank-model", "", "Rerank model id")
	cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0.0, "Generation temperature (0.0..2.0)")
	cmd.Flags().StringSliceVar(&opts.AddKBs, "add-kb", nil, "Attach knowledge base id (repeatable, idempotent)")
	cmd.Flags().StringSliceVar(&opts.RemoveKBs, "remove-kb", nil, "Detach knowledge base id (repeatable, idempotent)")
	cmd.Flags().StringVar(&opts.KBSelectionMode, "kb-selection-mode", "", "KB selection mode: all | selected | none")

	// Full-replace
	cmd.Flags().StringVar(&configFile, "config-file", "", "Full AgentConfig YAML or JSON (REPLACES current config baseline; surgical flags then apply on top)")

	cmdutil.AddJSONFlags(cmd, agentViewFields)
	return cmd
}

// editHasAnyFlag reports whether opts carries at least one surgical update
// signal. Required-flag validation lives in runEdit (not PreRunE) so unit
// tests can invoke runEdit with a hand-built EditOptions directly.
func editHasAnyFlag(opts *EditOptions) bool {
	fl := opts.flags
	return fl.nameSet || fl.descriptionSet || fl.modelSet || fl.systemPromptSet ||
		fl.agentModeSet || fl.rerankModelSet || fl.temperatureSet ||
		fl.addKBsSet || fl.removeKBsSet || fl.kbSelectionModeSet || fl.configFileSet
}

func runEdit(ctx context.Context, opts *EditOptions, jopts *cmdutil.JSONOptions, svc EditService) error {
	if !editHasAnyFlag(opts) {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "agent edit requires at least one flag",
			Hint:    "pass at least one update flag (e.g., --name, --add-kb, --description) or --config-file",
		}
	}

	// Fetch-then-update so omitted fields round-trip unchanged through
	// the full PUT body.
	current, err := svc.GetAgent(ctx, opts.AgentID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch agent %s", opts.AgentID)
	}

	// Build base config: server state, then overlay --config-file (if any).
	base := sdk.AgentConfig{}
	if current.Config != nil {
		base = *current.Config
	}
	if opts.ConfigFileBody != nil {
		parsed, err := cmdutil.LoadAgentConfig(opts.ConfigFileBody, opts.ConfigFileKind)
		if err != nil {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, err.Error())
		}
		base = *parsed
	}

	// Resolve --system-prompt-file before flag overlay.
	if opts.SystemPromptReader != nil {
		body, err := io.ReadAll(opts.SystemPromptReader)
		if err != nil {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("--system-prompt-file read: %v", err))
		}
		opts.SystemPrompt = strings.TrimSpace(string(body))
	}

	// Compute KB list from current + add/remove with a stderr warning when
	// the same id appears in both flags (net no-op, still idempotent).
	kbs := computeKBList(base.KnowledgeBases, opts.AddKBs, opts.RemoveKBs)

	overrides := cmdutil.AgentConfigFlags{
		AgentMode: opts.AgentMode, AgentModeSet: opts.flags.agentModeSet,
		SystemPrompt: opts.SystemPrompt, SystemPromptSet: opts.flags.systemPromptSet,
		ModelID: opts.Model, ModelIDSet: opts.flags.modelSet,
		RerankModelID: opts.RerankModel, RerankModelIDSet: opts.flags.rerankModelSet,
		Temperature: opts.Temperature, TemperatureSet: opts.flags.temperatureSet,
		KBSelectionMode: opts.KBSelectionMode, KBSelectionModeSet: opts.flags.kbSelectionModeSet,
		// KB list is always replaced (its add/remove was already merged
		// into kbs); we only signal "set" when the user actually touched
		// the list so a plain --name edit doesn't churn the field.
		KnowledgeBases:    kbs,
		KnowledgeBasesSet: opts.flags.addKBsSet || opts.flags.removeKBsSet,
	}
	cfg := cmdutil.MergeAgentConfig(&base, overrides)

	// Build the full PUT body. Name/Description default to the current
	// server values so the surgical-flag-only path preserves them.
	req := &sdk.UpdateAgentRequest{
		Name:        current.Name,
		Description: current.Description,
		Config:      cfg,
	}
	if opts.flags.nameSet {
		req.Name = opts.Name
	}
	if opts.flags.descriptionSet {
		req.Description = opts.Description
	}

	updated, err := svc.UpdateAgent(ctx, opts.AgentID, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "edit agent %s", opts.AgentID)
	}
	return emitAgent(jopts, updated)
}

// computeKBList applies --add-kb / --remove-kb to current with idempotent
// semantics. Ids present in both add and remove cancel out and surface a
// stderr warning so users notice the conflict but don't see a hard error.
// Stderr is the right channel here (not stdout) because callers piping
// --json | jq would otherwise see corrupted JSON.
func computeKBList(current, add, remove []string) []string {
	// Detect ids in both add and remove; they net out to no-op and are
	// excluded from both operations.
	canceledSet := map[string]struct{}{}
	addSeen := map[string]struct{}{}
	for _, id := range add {
		addSeen[id] = struct{}{}
	}
	for _, id := range remove {
		if _, both := addSeen[id]; both {
			canceledSet[id] = struct{}{}
		}
	}
	if len(canceledSet) > 0 {
		canceled := make([]string, 0, len(canceledSet))
		for id := range canceledSet {
			canceled = append(canceled, id)
		}
		// Sort for deterministic test output; map iteration is random.
		sort.Strings(canceled)
		fmt.Fprintf(iostreams.IO.Err, "warning: --add-kb and --remove-kb cancel out for: %s\n", strings.Join(canceled, ", "))
	}

	// Compute effective remove set (excluding canceled).
	removeEff := map[string]struct{}{}
	for _, id := range remove {
		if _, c := canceledSet[id]; c {
			continue
		}
		removeEff[id] = struct{}{}
	}

	// Filter removals out of the current list (idempotent: unattached id
	// simply isn't in current).
	out := make([]string, 0, len(current))
	for _, id := range current {
		if _, drop := removeEff[id]; drop {
			continue
		}
		out = append(out, id)
	}
	// Append any add ids not already present (idempotent: already-attached
	// id silently de-dupes).
	present := map[string]struct{}{}
	for _, id := range out {
		present[id] = struct{}{}
	}
	for _, id := range add {
		if _, c := canceledSet[id]; c {
			continue
		}
		if _, dup := present[id]; dup {
			continue
		}
		out = append(out, id)
		present[id] = struct{}{}
	}
	return out
}
