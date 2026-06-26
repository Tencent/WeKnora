package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbInitFields enumerates the fields surfaced for `--format json` discovery on
// `kb init`. The result is the resulting InitializationConfig (read back).
var kbInitFields = []string{
	"chat_model_id", "embedding_model_id", "rerank_model_id", "multimodal_id",
}

type InitOptions struct {
	ChatModel      string
	EmbeddingModel string
	Yes            bool
	DryRun         bool
}

// InitService is the narrow SDK surface this command depends on. SetKBModelConfig
// points the KB at already-registered models; GetInitializationConfig re-reads
// the server's resulting state so the success envelope reflects what stuck.
type InitService interface {
	SetKBModelConfig(ctx context.Context, kbID string, cfg *sdk.KBModelConfig) error
	GetInitializationConfig(ctx context.Context, kbID string) (*sdk.InitializationConfig, error)
}

// NewCmdInit builds `weknora kb init <kb-id>` — bind models to a knowledge base
// so it becomes usable for retrieval and generation.
func NewCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &InitOptions{}
	cmd := &cobra.Command{
		Use:   "init <kb-id>",
		Short: "Configure a knowledge base's models (make it usable)",
		Long: `Bind already-registered models to a knowledge base so it can embed, retrieve,
and generate. Both --chat-model (LLM, used for generation/summary) and
--embedding-model (used for retrieval) are required; register models first with
'weknora model create' and discover ids with 'weknora model list'.

High-risk write: changing a KB's embedding model affects how its content is
indexed and searched (and the server refuses once the KB has documents).
Without -y/--yes in a non-TTY / JSON context it exits 10
(input.confirmation_required) without applying the change.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			kbID := args[0]
			// Validate required flags before the dry-run gate so --dry-run rejects
			// identically to the live path.
			if err := validateInitFlags(opts); err != nil {
				return err
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "kb.init",
				Args:   map[string]any{"kb": kbID, "chat_model": opts.ChatModel, "embedding_model": opts.EmbeddingModel},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(),
				"configure", "knowledge base", kbID, "kb.init",
				cmdutil.BuildRetryArgv(c, []string{"weknora", "kb", "init", kbID}, "chat-model", "embedding-model", "format")); err != nil {
				return err
			}
			// Resolve name-or-id for the model flags (a UUID passes through; a
			// name is looked up among models of the expected type). Network read
			// on the live path only — the dry-run above shows the raw refs.
			if opts.ChatModel, err = cmdutil.ResolveModelRef(c.Context(), cli, opts.ChatModel, "KnowledgeQA"); err != nil {
				return err
			}
			if opts.EmbeddingModel, err = cmdutil.ResolveModelRef(c.Context(), cli, opts.EmbeddingModel, "Embedding"); err != nil {
				return err
			}
			return runInit(c.Context(), opts, fopts, cli, kbID)
		},
	}
	cmd.Flags().StringVar(&opts.ChatModel, "chat-model", "", "Chat / LLM model id or name for generation & summary (required) — see `weknora model list`")
	cmd.Flags().StringVar(&opts.EmbeddingModel, "embedding-model", "", "Embedding model id or name for retrieval (required) — see `weknora model list`")
	cmdutil.AddFormatFlag(cmd, kbInitFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "kb.init")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "bind models to a KB so it becomes usable. --chat-model and --embedding-model are required and accept a model id or name; discover them with `weknora model list`.",
		RequiredFlags: []string{"<kb-id> (positional)", "--chat-model", "--embedding-model"},
		Examples: []string{
			"weknora kb init kb_abc --chat-model model_llm --embedding-model model_emb -y",
		},
		Output: "envelope.data is the resulting {chat_model_id, embedding_model_id, rerank_model_id, multimodal_id}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"The server refuses to change the embedding model of a KB that already has documents.",
		},
	})
	return cmd
}

func validateInitFlags(opts *InitOptions) error {
	var missing []string
	if strings.TrimSpace(opts.ChatModel) == "" {
		missing = append(missing, "--chat-model")
	}
	if strings.TrimSpace(opts.EmbeddingModel) == "" {
		missing = append(missing, "--embedding-model")
	}
	if len(missing) == 0 {
		return nil
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeInputMissingFlag,
		Message: "kb init requires " + strings.Join(missing, " and "),
		Hint:    "discover model ids with `weknora model list` (or register one with `weknora model create`), then pass --chat-model <id> --embedding-model <id>",
	}
}


func runInit(ctx context.Context, opts *InitOptions, fopts *cmdutil.FormatOptions, svc InitService, kbID string) error {
	if err := validateInitFlags(opts); err != nil {
		return err
	}
	cfg := &sdk.KBModelConfig{
		LLMModelID:       opts.ChatModel,
		EmbeddingModelID: opts.EmbeddingModel,
	}
	if err := svc.SetKBModelConfig(ctx, kbID, cfg); err != nil {
		return cmdutil.WrapHTTP(err, "configure knowledge base %q", kbID)
	}
	// Re-read the server's resulting state so the envelope reflects what stuck.
	result, err := svc.GetInitializationConfig(ctx, kbID)
	if err != nil || result == nil {
		// The write succeeded; surface what we applied if the read-back failed.
		result = &sdk.InitializationConfig{ChatModelID: opts.ChatModel, EmbeddingModelID: opts.EmbeddingModel}
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Configured knowledge base %s (chat: %s, embedding: %s)\n",
		kbID, result.ChatModelID, result.EmbeddingModelID)
	return nil
}

// compile-time check: the production SDK client implements InitService.
var _ InitService = (*sdk.Client)(nil)
